package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"gofr.dev/pkg/gofr"
	gofrHTTP "gofr.dev/pkg/gofr/http"

	// Kubernetes client imports
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured" // Add this import
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// ExternalSecretGVR defines the GroupVersionResource for ExternalSecret
var ExternalSecretGVR = schema.GroupVersionResource{
	Group:    "external-secrets.io",
	Version:  "v1beta1",
	Resource: "externalsecrets",
}

// BasicAuth credentials as GoFr environment variables
var (
	basicAuthUser           = os.Getenv("BASIC_AUTH_USER")
	basicAuthPassword       = os.Getenv("BASIC_AUTH_PASSWORD")
	enableCacheBuster       = os.Getenv("ENABLE_CACHE_BUSTER") == "true"
	cacheBusterWaitInterval = 2 * time.Second
)

var dynamicNewForConfig = dynamic.NewForConfig

var dynamicClientCreator = createDynamicClient

func createDynamicClient(config *rest.Config) (dynamic.Interface, error) {
	return dynamicNewForConfig(config)
}

// Event represents the incoming webhook event
type Event struct {
	EventID    int               `json:"event_id,omitempty"`
	EventLevel string            `json:"event_level,omitempty"`
	EventType  string            `json:"event_type,omitempty"`
	ItemName   string            `json:"item_name,omitempty"`
	ItemID     int               `json:"item_id,omitempty"`
	ItemType   string            `json:"item_type,omitempty"`
	Payload    map[string]string `json:"payload,omitempty"`
}

// We are making sure that the content type is set to "application/json; charset=utf-8"
// so that the WebhookHandler can parse the incoming events
func customMiddleware() gofrHTTP.Middleware {
	return func(inner http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check if the Content-Type header is not set to application/json
			if r.Header.Get("Content-Type") != "application/json; charset=utf-8" {
				// Set the Content-Type header to application/json
				r.Header.Set("Content-Type", "application/json; charset=utf-8")
			}

			// Call the next handler in the chain
			inner.ServeHTTP(w, r)
		})
	}
}

func main() {
	// Ensure that BASIC_AUTH_USER and BASIC_AUTH_PASSWORD are set
	if basicAuthUser == "" || basicAuthPassword == "" {
		log.Fatal("Error: BASIC_AUTH_USER and BASIC_AUTH_PASSWORD environment variables must be set.")
	}

	// Create a new GoFr app
	app := gofr.New()

	// Add your custom middleware to the application
	app.UseMiddleware(customMiddleware())

	// Register middleware for basic authentication
	app.EnableBasicAuth(basicAuthUser, basicAuthPassword)

	// Define the route for webhook events
	app.POST("/webhook", WebhookHandler)

	// Start the GoFr app
	app.Run()
}

// WebhookHandler is the main handler for incoming webhook requests
// It processes the incoming events and triggers the patching of ExternalSecrets if necessary.
func WebhookHandler(ctx *gofr.Context) (interface{}, error) {
	// Get the cache buster wait interval from the environment variable
	cacheBusterWaitInterval = getEnvDuration(ctx, "CACHE_BUSTER_WAIT_INTERVAL", 2*time.Second)

	// Decode the incoming webhook event into a slice of Event structs
	var events []Event
	if err := ctx.Bind(&events); err != nil {
		ctx.Logger.Errorf("Failed to bind incoming events: %v", err)
		return nil, err
	}

	// Log the entire request details
	logRequestDetails(ctx, events)

	// Check if there are any events to process
	if len(events) > 0 {
		event := events[0]
		ctx.Logger.Infof("Received event for secret update: %s\n", event.ItemName)

		// Attempt to patch the ExternalSecret in Kubernetes based on the event
		if err := patchExternalSecret(ctx, event.ItemName); err != nil {
			ctx.Logger.Errorf("Error patching ExternalSecret: %v", err)
		}
	}

	// Respond with success
	return nil, nil
}

// patchExternalSecret looks for ExternalSecrets that match the incoming event and patches them
// It logs the process and any errors encountered during the operation.
func patchExternalSecret(ctx *gofr.Context, itemName string) error {
	// Create Kubernetes client configuration
	config, err := rest.InClusterConfig()
	if err != nil {
		// Fallback to kubeconfig for local development
		kubeconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			clientcmd.NewDefaultClientConfigLoadingRules(),
			&clientcmd.ConfigOverrides{},
		)
		config, err = kubeconfig.ClientConfig()
		if err != nil {
			ctx.Logger.Fatalf("Failed to load kubeconfig: %v", err)
			return err
		}
	}

	// Create a dynamic Kubernetes client
	dynamicClient, err := dynamicClientCreator(config)
	if err != nil {
		ctx.Logger.Fatalf("Failed to create dynamic Kubernetes client: %v", err)
	}

	// Retrieve the namespace from the in-cluster config
	namespace, err := getNamespace()
	if err != nil {
		ctx.Logger.Errorf("Failed to get namespace: %v", err)
		namespace = "default" // Fallback to default if retrieval fails
	}

	// List all ExternalSecrets in the namespace
	externalSecrets, err := dynamicClient.Resource(ExternalSecretGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		ctx.Logger.Fatalf("Failed to list ExternalSecrets in namespace %s: %v", namespace, err)
	}

	// Iterate over each ExternalSecret and process it
	for _, es := range externalSecrets.Items {
		name := es.GetName()
		ctx.Logger.Infof("Processing ExternalSecret: %s\n", name)

		// Access the spec field
		spec, found, err := unstructured.NestedMap(es.Object, "spec")
		if err != nil || !found {
			ctx.Logger.Errorf("Error retrieving spec for ExternalSecret %s: %v\n", name, err)
			continue
		}

		// Access the data field, which is a slice
		dataList, found, err := unstructured.NestedSlice(spec, "data")
		if err != nil || !found {
			ctx.Logger.Errorf("spec.data not found for ExternalSecret %s: %v\n", name, err)
			continue
		}

		keyFound := false
		for _, item := range dataList {
			dataMap, ok := item.(map[string]interface{})
			if !ok {
				ctx.Logger.Errorf("Invalid data item in ExternalSecret %s\n", name)
				continue
			}

			remoteRef, found, err := unstructured.NestedMap(dataMap, "remoteRef")
			if err != nil || !found {
				ctx.Logger.Errorf("remoteRef not found in data item of ExternalSecret %s: %v\n", name, err)
				continue
			}

			key, found, err := unstructured.NestedString(remoteRef, "key")
			if err != nil || !found {
				ctx.Logger.Errorf("key not found in remoteRef of ExternalSecret %s: %v\n", name, err)
				continue
			}

			ctx.Logger.Infof("Found key in ExternalSecret %s: %s\n", name, key)

			if key == itemName {
				keyFound = true
				break
			}
		}

		if keyFound {
			ctx.Logger.Infof("Desired key found in ExternalSecret %s\n", name)
			if err := updateExternalSecret(ctx, dynamicClient, &es, namespace); err != nil {
				ctx.Logger.Errorf("Failed to update ExternalSecret %s: %v\n", name, err)
				return err
			}
			ctx.Logger.Infof("Successfully updated ExternalSecret %s\n", name)
		} else {
			ctx.Logger.Infof("Desired key '%s' not found in ExternalSecret %s\n", itemName, name)
			return nil
		}
	}
	return nil
}

func updateExternalSecret(ctx *gofr.Context, dynamicClient dynamic.Interface, es *unstructured.Unstructured, namespace string) error {
	name := es.GetName()

	// Function to update annotations and perform the update
	updateFunc := func(es *unstructured.Unstructured) error {
		annotations := es.GetAnnotations()
		if annotations == nil {
			annotations = make(map[string]string)
		}

		annotations["updated-by"] = "externalsecret-updater"
		annotations["updated-at"] = time.Now().Format(time.RFC3339)
		es.SetAnnotations(annotations)

		ctx.Logger.Infof("Updating ExternalSecret %s in namespace %s", name, namespace)
		_, err := dynamicClient.Resource(ExternalSecretGVR).Namespace(namespace).Update(ctx, es, metav1.UpdateOptions{})
		if err != nil {
			ctx.Logger.Errorf("Failed to update ExternalSecret %s: %v", name, err)
			return err
		}
		ctx.Logger.Infof("Successfully updated ExternalSecret %s", name)
		return nil
	}

	// Perform the first update
	if err := updateFunc(es); err != nil {
		return err
	}

	if enableCacheBuster {
		ctx.Logger.Infof("Cache buster enabled. Waiting for %v before second update", cacheBusterWaitInterval)
		time.Sleep(cacheBusterWaitInterval)

		// Fetch the latest version of the ExternalSecret
		latestES, err := dynamicClient.Resource(ExternalSecretGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			ctx.Logger.Errorf("Failed to fetch latest ExternalSecret %s: %v", name, err)
			return err
		}

		ctx.Logger.Infof("Performing second update on ExternalSecret %s to bust cache", name)
		if err := updateFunc(latestES); err != nil {
			return err
		}
		ctx.Logger.Infof("Successfully performed second update on ExternalSecret %s", name)
	} else {
		ctx.Logger.Info("Cache buster is disabled")
	}

	return nil
}

// Helper function to get duration from environment variable with a default value
func getEnvDuration(ctx *gofr.Context, key string, defaultValue time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		ctx.Logger.Errorf("Invalid duration for %s, using default: %v", key, err)
		return defaultValue
	}
	return duration
}

// logRequestDetails logs the entire request details
func logRequestDetails(ctx *gofr.Context, events []Event) {
	ctx.Logger.Debugf("Received events: %v", events)
}

// getNamespace retrieves the namespace from the in-cluster configuration
// It reads the namespace from the file that Kubernetes mounts.
func getNamespace() (string, error) {
	data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
