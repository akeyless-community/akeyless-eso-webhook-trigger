# Akeyless External Secrets Updater

This is a simple application that listens for a webhook and then updates an ExternalSecret resource in a Kubernetes cluster in order to propagate a secret from Akeyless to the cluster as a push operation instead of a constant pull operations.

## Prerequisites

- A Kubernetes cluster
- Akeyless credentials
- Akeyless Gateway API URL
# akeyless-eso-webhook-trigger
