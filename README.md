[![go.dev reference](https://img.shields.io/badge/go.dev-reference-007d9c?logo=go&logoColor=white&style=flat-square)](https://pkg.go.dev/github.com/replicatedhq/kots)


# Kubernetes Off-The-Shelf (KOTS) Software
Replicated KOTS is the collective set of tools that enable the distribution and management of Kubernetes Off-The-Shelf (KOTS) software. The Kots CLI (a Kubectl plugin) is a general purpose, client-side binary for configuring and building dynamic Kubernetes manifests. The Kots CLI also serves as the bootstrapper for the in-cluster Kubernetes application Admin Console [kotsadm](https://github.com/replicatedhq/kots/tree/master/kotsadm) which can be used to automate the core Kots CLI tasks for managing applications (license verification, configuration, updates, image renaming, version controlling changes, and deployment) as well as additional KOTS tasks (running preflight checks and performing support bundle analysis).

## Distributing a KOTS application
Software vendors can [package their Kubernetes applications](https://kots.io/vendor/) or [Helm charts](https://kots.io/vendor/helm/using-helm-charts) or [Operators](https://kots.io/vendor/operators/packaging-an-operator/) as a KOTS application in order to distribute the application to cluster operators.

## Kots CLI Documentation
Check out the [full docs on the cluster operator experience](https://kots.io/kots-cli/getting-started/) for using the Kots CLI as a Kubectl plugin.

## Try Kots
Try Kots as a cluster operator by installing the Replicated sample app ([Sentry Pro Example](https://github.com/replicatedhq/kots-sentry/)) into an existing Kubernetes cluster. First, install the Kots CLI (a Kubectl plugin) on your workstation:
```
curl https://kots.io/install | bash
```

### Run `kots install`
The `install` command is the recommended way to learn KOTS. Executing the `install` command will install an application and the [kotsadm](https://github.com/replicatedhq/kotsadm) Admin Console to an existing Kubernetes cluster. This command supports installing Helm charts (without Tiller), standard Kubernetes applications and also Replicated KOTS apps.

Continue with the demo by running the following command:
```
kubectl kots install sentry-pro
```

Set a namespace for the admin console and the application components to be installed, and provide a password for the admin console. After this command completes, the kotsadm Admin Console will be running in your cluster, listening on port :8800 on a ClusterIP service in the namespace you deployed the application to. By default this is exposed to your workstation using kubectl port-forward, but you could set up an ingress/load balancer of your own.

### Access the Admin Console
Visit http://localhost:8800 to access the Admin Console, enter the password.

Download the [sample license](https://kots.io/sample-license) for Sentry Pro & upload it to the console. You'll then be presented with configuration settings, preflight checks and other application options.

If you terminate your terminal session, the port-forward will also terminate. To access the admin console again, just run:
```
kubectl kots admin-console --namespace sentry-pro
```

# Community

For questions about using KOTS, there's a [Replicated Community](https://help.replicated.com/community) forum, and a [#kots channel in Kubernetes Slack](https://kubernetes.slack.com/channels/kots).

# Notifications

If KOTS is installed into an existing Kubernetes cluster, by default KOTS will leverage [MinIO](https://github.com/minio/minio) as a standalone object store instance to store application archives and support bundles. All communication between KOTS and the MinIO object store is limited to a REST API released under the Apache 2.0 license. KOTS has not modified the MinIO source code. Use of [MinIO](https://github.com/minio/minio) is currently governed by the GNU AGPLv3 license that can be found in their [LICENSE](https://github.com/minio/minio/blob/master/LICENSE) file. To remove MinIO usage for this use case, an optional install flag `--with-minio=false` is available for new [KOTS installs](https://kots.io/kots-cli/install/) or [upgrades from existing versions](https://kots.io/kots-cli/admin-console/upgrade/).

# Software Bill of Materials
Signed SBOMs for KOTS Go dependencies and are included in each release. 
Use [Cosign](https://github.com/sigstore/cosign) to validate the signature by running the following
command.
```shell
cosign verify-blob -key sbom/key.pub -signature sbom/kots-sbom.tgz.sig sbom/kots-sbom.tgz
```
