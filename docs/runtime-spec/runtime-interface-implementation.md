# Runtime 接口拆分与 containerd 取数路径

本文说明 `pkg/runtime` 中容器详情相关接口的职责边界，以及 containerd 实现对应的数据来源。

## 接口拆分原则

- **`GetContainerDetail`**：返回容器详情页基础概览，以及和容器主视图/进程摘要强相关的信息。
- **`GetContainerRuntimeInfo`**：只返回 runtime / shim / OCI / namespace 视角的数据。
- **`GetContainerStorageInfo`**：只返回 snapshot / rootfs / mounts / RW layer 视角的数据。
- **`GetContainerNetworkInfo`**：只返回 sandbox / netns / CRI/CNI 网络视角的数据。

拆分依据：

1. **相关性**：避免把 runtime、存储、网络三类数据混在一个接口里。
2. **containerd 取数路径**：将和 `GetContainerDetail` 共享 `LoadContainer -> Info -> Task -> Spec` 路径的基础数据下沉到 `GetContainerDetail`，减少重复拼装。

## containerd 实现映射

### 1. `GetContainerDetail`

主要用于详情页概览、进程摘要和通用基础信息展示。

数据来源：

- `client.LoadContainer` + `Container.Info`
  - `ContainerDetail.Container`
  - `ImageName`
  - `SnapshotKey`
  - `Snapshotter`
- `Container.Task`
  - `PID`
  - `Status`
- `Container.Spec`
  - `Namespaces`
  - `CGroupPath`
  - `Environment`
  - `SharedPID`
- `cgroupReader.ReadCGroupLimits`
  - `CGroupLimits`
  - `CGroupVersion`
- `processCollector.CollectContainerProcesses`
  - `ProcessCount`
- `criClient.InspectContainerStatus`
  - `RestartCount`
  - `ExitedAt`
  - `ExitCode`
  - `ExitReason`
  - `SharedPID` / `Environment` 的补充信息

### 2. `GetContainerRuntimeInfo`

用于 Runtime 子页，仅聚焦 runtime 元数据。

数据来源：

- `Container.Spec`
  - `Namespaces`
  - `CGroupPath`
- `Container.Task` + `/proc`
  - `ShimPID`
- runtime v2 约定目录、bundle 文件与 `/proc`
  - `RuntimeProfile.OCI`
  - `RuntimeProfile.Shim`
  - `RuntimeProfile.CGroup`
  - `OCIBundlePath`
  - `OCIRuntimeDir`

### 3. `GetContainerStorageInfo`

用于 Filesystem / Mounts / Layers 子页。

数据来源：

- `Container.Info`
  - `SnapshotKey`
  - `Snapshotter`
- `SnapshotService(...).Mounts/Usage`
  - `WritableLayerPath`
  - `RWLayerUsage`
  - `RWLayerInodes`
- `resolveContainerMounts`
  - `Mounts`
  - `MountCount`
  - `RuntimeProfile.RootFS`

### 4. `GetContainerNetworkInfo`

用于 Network 子页。

数据来源：

- `Container.Spec`
  - network namespace path
- `criClient.InspectPodSandboxNetwork`
  - `PodNetwork`
  - `IPAddress`
  - `PortMappings`
- `/proc/<pid>/net/dev`
  - `PodNetwork.ObservedInterfaces`

## 调用建议

- 容器详情主刷新优先调用 `GetContainerDetail`。
- Runtime / Storage / Network 子页按需调用各自接口。
- 如果详情页概览需要补充子页专属字段，可再分别合并三个子接口的返回值，但不要再依赖单个“大而全”的 runtime 接口。
