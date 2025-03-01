# MT CSI Driver for 3FS

This is a Container Storage Interface ([CSI](https://github.com/container-storage-interface/spec/blob/master/spec.md)) for [3FS](https://github.com/deepseek-ai/3FS) storage.

## Kubernetes installation

### Requirements

* Kubernetes 1.23+
* Kubernetes has to allow privileged containers
* Docker daemon must allow shared mounts (systemd flag `MountFlags=shared`)
* Deepseek 3FS is deployed and the client is complete

### Manual installation

#### 1. Build csi driver image

```sh
make image
```

#### 2. Deploy the driver

```bash
cd deploy
kubectl apply -f csi-provisioner.yaml
kubectl apply -f csi-driver.yaml
kubectl apply -f csi-3fs.yaml
kubectl apply -f csi-storageclass.yaml
```

#### 3. Deploy the pvc and pod

```bash
cd examples
kubectl apply -f pvc.yaml
kubectl apply -f pod.yaml
```

#### 4. Check the results

```bash
$ kubectl get csidriver 
NAME                   ATTACHREQUIRED   PODINFOONMOUNT   STORAGECAPACITY   TOKENREQUESTS   REQUIRESREPUBLISH   MODES        AGE
3fs.csi.mthreads.com   true             true             false             <unset>         false               Persistent   73m

$ kubectl get sc
NAME      PROVISIONER            RECLAIMPOLICY   VOLUMEBINDINGMODE   ALLOWVOLUMEEXPANSION   AGE
csi-3fs   3fs.csi.mthreads.com   Delete          Immediate           false                  59m

$ kubectl get pvc -n default
NAMESPACE   NAME    STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS   AGE
default     demo0   Bound    pvc-52b8d8bc-f2e4-4a19-8aab-cb697e46de8f   1Gi        RWX            csi-3fs        47m
```

