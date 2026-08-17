**The GitOps Operator Deployment Workflow**
Instead of running the controller loop manually via your Mac's terminal shell, you will build a local container image and let your cluster run the control plane natively.

**1. Build & Load the Operator Image to Kind**
Since you are using Kind, you don't even need to push your image to a public registry like Docker Hub. You can build it locally and inject it straight into your Kind nodes:

```bash
# 1. Build the manager container image locally
make docker-build IMG=mlo.platform/local-llm-inference-control-plane:v1

# 2. Side-load the image directly into your M5 Kind cluster nodes
kind load docker-image mlo.platform/local-llm-inference-control-plane:v1 --name m5-platform
```

**2. Export the Production Manifests**
Kubebuilder creates all the deployment manifests you need to run your operator inside the cluster under the config/ directory. Generate the final production YAML bundle:

```bash
# Render all controller manifests (Deployment, RBAC, CRDs) into a single file
kubectl kustomize config/default > ~/gitops-infra/system-operators/operator-bundle.yaml
```
*Note: Open ~/gitops-infra/operator-bundle.yaml, search for image: controller:latest, and change it to your loaded image: image: mlo.platform/local-llm-inference-control-plane:v1 so the cluster knows where to pull it).*

**3. Let ArgoCD Manage Everything**
Update your GitHub repository to track both the core operator infrastructure and your custom workloads. Your final production GitOps repository structure will look like this:

```text
📁 gitops-infra/
├── 📁 system-operators/
│   └── operator-bundle.yaml       <-- Runs your Go Operator container in-cluster
└── 📁 apps/
    └── sample-llm.yaml            <-- Your application developer manifest
```
