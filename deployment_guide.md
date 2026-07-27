# Kubernetes & Kind Deployment Guide

This guide provides instructions to build, load, and deploy the E-Commerce platform in a local Kubernetes cluster using **Kind** (Kubernetes in Docker).

---

## Prerequisites

Ensure you have the following installed on your machine:
1. **Docker Desktop** (or Docker Engine)
2. **Go** (if you need to run/build custom Go binary schedulers)
3. **Kind CLI** ([Installation Guide](https://kind.sigs.k8s.k8s.io/docs/user/quick-start/))
4. **Kubectl CLI** ([Installation Guide](https://kubernetes.io/docs/tasks/tools/))
5. **Node.js** (for running the load generator script locally)

---

## Step 1: Create a Kind Cluster with a Multi-Node Topology

We will create a 2-node cluster (1 control plane, 1 worker node) to properly test scheduling pressure.

1. Create a configuration file named `kind-config.yaml` or run the command below:
   ```bash
   cat <<EOF > kind-config.yaml
   apiVersion: kind.x-k8s.io/v1alpha4
   kind: Cluster
   nodes:
     - role: control-plane
     - role: worker
   EOF
   ```
2. Spin up the cluster:
   ```bash
   kind create cluster --config kind-config.yaml
   ```
3. Verify your nodes are active:
   ```bash
   kubectl get nodes
   ```

---

## Step 2: Install Kubernetes Metrics Server

By default, Kind clusters do not come with the **Metrics Server** (which aggregates actual resource utilization). Your custom scheduler needs this to detect idle pods.

1. Apply the official Metrics Server manifest:
   ```bash
   kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml
   ```
2. **Important for Kind:** Since local testing uses self-signed certificates, we must patch the Metrics Server to run in insecure mode. Patch the deployment:
   ```bash
   kubectl patch deployment metrics-server -n kube-system --type='json' -p='[{"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--kubelet-insecure-tls"}]'
   ```
3. Wait for the metrics server to start:
   ```bash
   kubectl get deployment metrics-server -n kube-system
   ```

---

## Step 3: Build & Load Docker Images

Kind doesn't have access to your local computer's Docker daemon by default. We must build the images locally and then "load" them into the Kind cluster nodes.

1. Navigate to the root directory `cloud-ecommerce/`:
   ```bash
   cd cloud-ecommerce
   ```
2. Build the Docker images:
   ```bash
   docker build -t frontend:latest ./frontend
   docker build -t nginx-custom:latest ./nginx
   docker build -t api-gateway:latest ./api-gateway
   docker build -t user-service:latest ./user-service
   docker build -t product-service:latest ./product-service
   docker build -t order-service:latest ./order-service
   docker build -t notification-service:latest ./notification-service
   docker build -t worker:latest ./worker
   docker build -t analytics-service:latest ./analytics-service
   ```
3. Load the images into Kind:
   ```bash
   kind load docker-image frontend:latest
   kind load docker-image nginx-custom:latest
   kind load docker-image api-gateway:latest
   kind load docker-image user-service:latest
   kind load docker-image product-service:latest
   kind load docker-image order-service:latest
   kind load docker-image notification-service:latest
   kind load docker-image worker:latest
   kind load docker-image analytics-service:latest
   ```

---

## Step 4: Deploy Manifests to Kubernetes

All K8s templates are located in the `kubernetes/` folder. We will apply them in order.

1. **Deploy Namespace:**
   ```bash
   kubectl apply -f kubernetes/namespace/
   ```
2. **Deploy Secrets & ConfigMaps:**
   ```bash
   kubectl apply -f kubernetes/secrets/
   kubectl apply -f kubernetes/configmaps/
   ```
3. **Deploy Databases & Brokers (PostgreSQL, MongoDB, Redis, RabbitMQ, Kafka):**
   ```bash
   kubectl apply -f kubernetes/statefulsets/
   kubectl apply -f kubernetes/deployments/redis.yaml
   kubectl apply -f kubernetes/deployments/rabbitmq.yaml
   kubectl apply -f kubernetes/deployments/kafka.yaml
   ```
4. **Wait for Databases to Boot Up:**
   ```bash
   kubectl wait --namespace=ecommerce --for=condition=ready pod -l app=postgres --timeout=90s
   kubectl wait --namespace=ecommerce --for=condition=ready pod -l app=mongodb --timeout=90s
   ```
5. **Deploy Microservices & Routing Engine:**
   ```bash
   kubectl apply -f kubernetes/deployments/frontend.yaml
   kubectl apply -f kubernetes/deployments/nginx.yaml
   kubectl apply -f kubernetes/deployments/api-gateway.yaml
   kubectl apply -f kubernetes/deployments/user-service.yaml
   kubectl apply -f kubernetes/deployments/product-service.yaml
   kubectl apply -f kubernetes/deployments/order-service.yaml
   kubectl apply -f kubernetes/deployments/notification-service.yaml
   kubectl apply -f kubernetes/deployments/worker.yaml
   kubectl apply -f kubernetes/deployments/analytics-service.yaml
   ```
6. **Deploy Observability Dashboards (Prometheus, Grafana, Loki):**
   ```bash
   kubectl apply -f kubernetes/deployments/prometheus.yaml
   kubectl apply -f kubernetes/deployments/grafana.yaml
   kubectl apply -f kubernetes/deployments/loki.yaml
   ```

Verify all pods are up and running:
```bash
kubectl get pods -n ecommerce
```

---

## Step 5: Verify Metrics & Access the Platform

1. Check physical resources usage:
   ```bash
   kubectl top pods -n ecommerce
   ```
2. Port-forward the Nginx reverse proxy so you can visit the storefront webpage:
   ```bash
   kubectl port-forward svc/nginx -n ecommerce 8080:80
   ```
3. Open your browser and navigate to `http://localhost:8080`. You should see the E-Commerce catalog loaded from Redis/MongoDB cache.

---

## Step 5.1: Monitor Cluster using a GUI / UI Tool (Optional, Linux)

For a real-time visual interface showing pod states, logs, and namespaces, we recommend installing one of the following tools:

### Option A: OpenLens (Desktop GUI)
OpenLens autodetects your local Kind configuration credentials from `~/.kube/config`.
* **Installation (via Flatpak):**
  ```bash
  flatpak install flathub io.k8slens.Lens
  ```
* **Installation (via Snap):**
  ```bash
  sudo snap install lens --classic
  ```
Once opened, click on the cluster icon to view nodes, pods, and select the `ecommerce` namespace from the sidebar dropdown.

### Option B: k9s (Terminal UI - Extremely fast)
An interactive terminal dashboard for navigating resources.
* **Installation:**
  ```bash
  sudo snap install k9s
  # or on Arch Linux: sudo pacman -S k9s
  ```
* **Usage:**
  1. Open a terminal and run `k9s`.
  2. Type `:ns` then hit `Enter` to view namespaces and select `ecommerce`.
  3. Type `:pods` then hit `Enter` to view all running pods.
  4. Use arrow keys to select a pod: press `l` to view logs, or `s` to execute a shell terminal inside it.

---

## Step 6: Load Testing & Custom Scheduler Verification

To evaluate your custom scheduler, you need to create resource exhaustion pressure:

1. **Deploy your custom scheduler** binary to the cluster so it monitors the pods labeled `adaptive-scheduler`.
2. **Start the load generator** to drive up traffic:
   ```bash
   cd load-generator
   npm install
   TARGET_URL=http://localhost:8080 CONCURRENCY=10 node load.js
   ```
3. **Deploy the Trigger AI Service** which requests `8 CPU`:
   ```bash
   kubectl apply -f kubernetes/deployments/trigger-ai-service.yaml
   ```
4. **Inspect Scheduler Logs:**
   Observe your scheduler detect idle class `A` replicas (workers and notification services) using the Metrics API, suspend them, and schedule the trigger workload.
