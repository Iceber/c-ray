# pkg/runtime 运行时接口实现说明

本文档说明 `pkg/runtime` 中具体运行时是如何实现 `runtime.Runtime` 接口的，重点覆盖：

- 每个接口方法依赖哪些数据源
- 每个返回字段是从哪里获取的
- 中间经过了哪些转换、回退和补全逻辑
- 当前哪些字段尚未由具体运行时填充

当前仓库里真正实现 `runtime.Runtime` 的是 `pkg/runtime/containerd.ContainerdRuntime`，因此本文档以 **containerd 实现** 为主。  
文档结构按“**接口契约 → 实现模板 → containerd 现状**”组织，后续新增其他运行时（如 CRI-O、docker）时，直接按同样章节补充即可。

---

## 1. `runtime.Runtime` 接口总览

接口定义位于 `pkg/runtime/interface.go`：

```go
type Runtime interface {
    Connect(ctx context.Context) error
    Close() error

    ListContainers(ctx context.Context) ([]*models.Container, error)
    GetContainer(ctx context.Context, id string) (*models.Container, error)
    GetContainerDetail(ctx context.Context, id string) (*models.ContainerDetail, error)
    GetContainerRuntimeInfo(ctx context.Context, id string) (*models.ContainerDetail, error)

    ListImages(ctx context.Context) ([]*models.Image, error)
    GetImage(ctx context.Context, ref string) (*models.Image, error)
    GetImageLayers(ctx context.Context, imageID, snapshotter, rwSnapshotKey string) ([]*models.ImageLayer, error)
    GetImageConfigInfo(ctx context.Context, imageID string) (*models.ImageConfigInfo, error)

    ListPods(ctx context.Context) ([]*models.Pod, error)
    GetContainerProcesses(ctx context.Context, id string) ([]*models.Process, error)
    GetContainerTop(ctx context.Context, id string) (*models.ProcessTop, error)
    GetContainerMounts(ctx context.Context, id string) ([]*models.Mount, error)
}
```

接口可以分成 4 类：

1. **连接生命周期**：`Connect` / `Close`
2. **容器与运行时详情**：`ListContainers` / `GetContainer` / `GetContainerDetail` / `GetContainerRuntimeInfo`
3. **镜像与 Pod**：`ListImages` / `GetImage` / `GetImageLayers` / `GetImageConfigInfo` / `ListPods`
4. **运行中容器的实时视图**：`GetContainerProcesses` / `GetContainerTop` / `GetContainerMounts`

---

## 2. 未来新增运行时时，建议沿用的文档模板

未来每增加一个运行时实现，建议新增一个同结构小节，至少回答下面 5 个问题：

### 2.1 运行时结构体依赖

说明实现类持有哪些依赖，以及每个依赖负责什么，例如：

| 字段 | 类型 | 作用 | 数据来源 |
|---|---|---|---|
| `client` | 运行时原生 client | 访问容器/镜像/任务 API | 运行时 socket |
| `procReader` | procfs 读取器 | 补全进程、网络、shim 信息 | `/proc` |
| `cgroupReader` | cgroup 读取器 | 读取 CPU/内存/PIDs 限制 | `/sys/fs/cgroup` |
| `mountReader` | mountinfo 读取器 | 读取 live mounts | `/proc/<pid>/mountinfo` |
| `metadataClient` | 可选元数据客户端 | 补充 CRI / runtime 专有字段 | runtime API / sidecar API |

### 2.2 每个接口方法的实现路径

每个方法都建议用固定格式描述：

1. **主入口**
2. **直接调用了哪些 API**
3. **有哪些 fallback**
4. **哪些字段会被填充**
5. **哪些字段故意不在该方法里填充**

### 2.3 返回模型字段映射表

建议按返回模型拆表，而不是只按函数描述。原因是同一个模型会被多个方法复用，但字段填充程度不同。

建议至少覆盖：

- `models.Container`
- `models.ContainerDetail`
- `models.Image`
- `models.ImageConfigInfo`
- `models.ImageLayer`
- `models.Pod`
- `models.Process`
- `models.ProcessTop`
- `models.Mount`
- `models.NetworkStats`

### 2.4 多源合并规则

如果一个字段可能来自多个来源，必须写清楚优先级。例如：

1. 原生 runtime 元数据
2. OCI spec
3. CRI metadata
4. procfs / mountinfo / cgroup
5. 推导值（convention / inferred）

### 2.5 未实现字段

对每个尚未填充的字段，建议显式写出：

- 当前返回默认零值/空值
- 为什么没有填
- 未来最可能从哪里补

---

## 3. 当前实现：`containerd.ContainerdRuntime`

### 3.1 结构体依赖与职责

`pkg/runtime/containerd/client.go` 中的实现如下：

| 字段 | 作用 | 获取方式 | 备注 |
|---|---|---|---|
| `config *runtime.Config` | 保存 socket、namespace、timeout 等配置 | `NewContainerdRuntime(config)` 入参 | 当前 `Timeout` 仅保存，未在 containerd client 调用里显式使用 |
| `client *client.Client` | containerd 原生 client | `Connect()` 中通过 `client.New(socket, client.WithDefaultNamespace(namespace))` 创建 | 所有 container/image/task API 都依赖它 |
| `processCollector *sysinfo.ProcessCollector` | 收集容器内进程、top、网络速率 | `NewProcessCollector()` | 内部又依赖 procfs/cgroup/sampler |
| `procReader *sysinfo.ProcReader` | 读取 `/proc` | `sysinfo.NewProcReader()` | 用于 shim 识别、网络接口、cgroup 路径等 |
| `cgroupReader *sysinfo.CGroupReader` | 读取 cgroup 限制 | `sysinfo.NewCGroupReader()` | 同时用于检测 cgroup v1/v2 |
| `mountReader *sysinfo.MountReader` | 读取 `/proc/<pid>/mountinfo` | `sysinfo.NewMountReader()` | 用于 live mounts 与 rootfs 推断 |
| `criClient criMetadataClient` | 读取 containerd 暴露的 CRI 元数据 | `runtimecri.NewClient(config.SocketPath)` | 补充 mounts、container status、PodSandbox network |

### 3.2 数据源分层

containerd 实现实际会组合 6 类数据源：

1. **containerd metadata API**
   - `client.Containers`
   - `LoadContainer`
   - `Container.Info`
   - `Container.Spec`
   - `Container.Task`
   - `ListImages` / `GetImage`
   - `ContentStore`
   - `SnapshotService`

2. **OCI spec**
   - `Container.Spec(ctx)` 返回的 `runtimespec.Spec`
   - 用于 namespaces、env、cgroup path、spec mounts

3. **CRI RuntimeService**
   - `ContainerStatus(verbose=true)`
   - `PodSandboxStatus(verbose=true)`
   - 同一个 containerd socket 上的 CRI gRPC 服务

4. **procfs**
   - `/proc/<pid>/stat`
   - `/proc/<pid>/cmdline`
   - `/proc/<pid>/status`
   - `/proc/<pid>/io`
   - `/proc/<pid>/net/dev`
   - `/proc/<pid>/cgroup`
   - `/proc/<pid>/exe`
   - `/proc/<pid>/cwd`

5. **mountinfo**
   - `/proc/<pid>/mountinfo`
   - 用于 live mounts、overlay rootfs 解析

6. **cgroup fs**
   - `/sys/fs/cgroup/...`
   - 读取 cpu/memory/pids/io 限制与使用量

---

## 4. 接口方法实现明细

## 4.1 `Connect(ctx)`

### 实现路径

1. 如果 `r.client != nil`，直接返回，表示已连接。
2. 使用：

   ```go
   client.New(
       r.config.SocketPath,
       client.WithDefaultNamespace(r.config.Namespace),
   )
   ```

3. 成功后把结果赋给 `r.client`。

### 配置字段使用方式

| 配置字段 | 用途 |
|---|---|
| `SocketPath` | 作为 containerd client 与 CRI client 的 unix socket 地址 |
| `Namespace` | 作为 containerd client 默认 namespace |
| `Timeout` | 当前实现中未直接传给 containerd API；调用方如果需要超时，依赖传入的 `ctx` |

---

## 4.2 `Close()`

### 实现路径

- 如果 `r.client != nil`，调用 `r.client.Close()`
- 否则直接返回 `nil`

这是纯资源释放，不涉及任何模型字段构建。

---

## 4.3 `ListContainers(ctx)`

### 实现路径

1. 调用 `r.client.Containers(ctx)` 获取全部 containerd 容器
2. 对每个容器调用 `convertContainer(ctx, c)`
3. 单个容器转换失败时直接跳过，不影响其他容器

### `convertContainer()` 的逻辑

1. `c.Info(ctx)` 读取 containerd metadata
2. `buildContainerFromInfo(info)` 先构造基础 `models.Container`
3. 再尝试 `c.Task(ctx, nil)`：
   - 成功：读取 `task.Pid()` 和 `task.Status(ctx)`
   - 失败：认为当前没有 task，状态直接标记为 `stopped`

### `models.Container` 字段来源（ListContainers / GetContainer 共用）

| 字段 | 来源 | 获取方式 | 逻辑 |
|---|---|---|---|
| `ID` | containerd metadata | `info.ID` | 直接赋值 |
| `Name` | labels / 推导 | 先读 `info.Labels["io.kubernetes.container.name"]`，再读 `info.Labels["name"]`，最后退化为 `info.ID[:12]` 或完整 `info.ID` | 有明确优先级 |
| `Image` | containerd metadata | `info.Image` | 直接赋值 |
| `ImageID` | 未填充 | - | 当前 containerd 实现未设置 |
| `Status` | task 状态 | `task.Status(ctx).Status` → `convertStatus()` | `created/running/paused/stopped` 映射到内部枚举；没有 task 时直接设为 `stopped`；读 task status 失败时设为 `unknown` |
| `CreatedAt` | containerd metadata | `info.CreatedAt` | 直接赋值 |
| `StartedAt` | 未在本方法填充 | - | 当前只会在 `GetContainerRuntimeInfo()` 通过 CRI status 补充 |
| `PID` | task | `task.Pid()` | 仅当 task 存在时有值 |
| `PodName` | labels | `info.Labels["io.kubernetes.pod.name"]` | 直接赋值 |
| `PodNamespace` | labels | `info.Labels["io.kubernetes.pod.namespace"]` | 直接赋值 |
| `PodUID` | labels | `info.Labels["io.kubernetes.pod.uid"]` | 直接赋值 |
| `Labels` | containerd metadata | `info.Labels` | 整体透传 |

---

## 4.4 `GetContainer(ctx, id)`

### 实现路径

1. `LoadContainer(ctx, id)`
2. 复用 `convertContainer(ctx, c)`

### 返回内容特点

- 字段填充规则与 `ListContainers()` 完全一致
- 这是“**单容器基础信息视图**”
- 不负责补齐 runtime profile、网络、挂载、CRI lifecycle 等增强字段

---

## 4.5 `GetContainerDetail(ctx, id)`

### 实现路径

1. `LoadContainer(ctx, id)`
2. `c.Info(ctx)` 读取基础 metadata
3. 构造：

   ```go
   detail := &models.ContainerDetail{
       Container: r.buildContainerFromInfo(info),
       ImageName: info.Image,
   }
   ```

4. 如果 `c.Task(ctx, nil)` 成功，则补齐 `PID` 和 `Status`
5. 如果没有 task，则把 `Status` 设为 `stopped`

### 返回内容特点

这个方法只返回“**详情页概览所需的轻量字段**”，不会做重型 runtime 探测。  
也就是说它只比 `GetContainer()` 多补一个 `ImageName`，并复用 `Container` 的 PID/Status 填充。

### `models.ContainerDetail` 在本方法中的填充范围

| 字段 | 填充情况 |
|---|---|
| `Container.*` | 已填充基础容器字段 |
| `ImageName` | 已填充，值为 `info.Image` |
| 其余 detail 字段 | 当前方法不填充，保留零值，交给 `GetContainerRuntimeInfo()` 或其他专用接口 |

---

## 4.6 `GetContainerRuntimeInfo(ctx, id)`

这是 containerd 实现里最完整、最关键的方法：它会把 **containerd metadata + OCI spec + CRI metadata + procfs + cgroup + mountinfo** 合并成一个增强版 `models.ContainerDetail`。

### 实现主流程

1. `LoadContainer(ctx, id)`
2. `c.Info(ctx)` 读取基础 metadata
3. 先创建：

   ```go
   detail := &models.ContainerDetail{
       Container:      r.buildContainerFromInfo(info),
       RuntimeProfile: &models.RuntimeProfile{},
   }
   ```

4. 预填基础 runtime 字段：
   - `ImageName = info.Image`
   - `SnapshotKey = info.SnapshotKey`
   - `Snapshotter = info.Snapshotter`

5. `c.Spec(ctx)` 获取 OCI spec，用于：
   - `Namespaces`
   - `CGroupPath`
   - `Environment`
   - `SharedPID`

6. `c.Task(ctx, nil)` 获取 task，用于：
   - `PID`
   - `Status`
   - `ShimPID`

7. 使用 snapshot service 补齐：
   - `WritableLayerPath`
   - `RWLayerUsage`
   - `RWLayerInodes`

8. 使用 cgroup reader 补齐：
   - `CGroupLimits`
   - `CGroupVersion`

9. 使用 CRI `InspectContainerStatus()` 补齐：
   - `StartedAt`
   - `ExitedAt`
   - `ExitCode`
   - `ExitReason`
   - `RestartCount`
   - `SharedPID`（只在 spec 没给出时回填）
   - `Environment`（只在 OCI spec 没有 env 时回填）

10. 使用 `resolveContainerMounts()` 合并：
    - CRI config/status mounts
    - OCI spec mounts
    - live mountinfo

11. 使用 `processCollector.CollectContainerProcesses()` 统计 `ProcessCount`
12. 使用 `populatePodNetwork()` 读取 PodSandbox 网络信息
13. 使用 `populateRuntimeProfile()` 构建 `RuntimeProfile`

---

### `models.ContainerDetail` 字段来源总表

#### 4.6.1 继承自 `models.Container` 的字段

这些字段与 `ListContainers()` / `GetContainer()` 中的来源一致，额外差异如下：

| 字段 | 增量逻辑 |
|---|---|
| `StartedAt` | 如果 CRI `ContainerStatus` 返回 `startedAt`，则在这里补齐 |
| `PID` / `Status` | 依然来自 `task.Pid()` / `task.Status()` |

#### 4.6.2 Process / lifecycle 字段

| 字段 | 来源 | 获取方式 | 逻辑 |
|---|---|---|---|
| `ProcessCount` | procfs | `processCollector.CollectContainerProcesses(detail.PID)` | 只统计数量，不直接把列表写入 `detail.Processes` |
| `Processes` | 未填充 | - | 进程明细通过 `GetContainerProcesses()` 单独返回 |
| `Environment` | OCI spec / CRI fallback | 先 `spec.Process.Env`，再在为空时使用 CRI `info.config.envs` | `KEY=VALUE` 用 `strings.Cut` 拆成 `models.EnvVar`；并标记 `IsKubernetes` |
| `SharedPID` | OCI spec / CRI fallback | 先扫描 `spec.Linux.Namespaces` 中 `pid` namespace 的 `Path`，若存在且非空则视为共享；若 spec 无结果，则回退到 CRI namespace options | 明确采用“spec 优先，CRI 回填” |
| `RestartCount` | CRI status | `status.metadata.attempt` | 仅 CRI 提供 |
| `ExitedAt` | CRI status | `status.finishedAt` | 纳秒时间戳转 `time.Time` |
| `ExitCode` | CRI status | `status.exitCode` | 只有 `finishedAt > 0` 时才会设置指针 |
| `ExitReason` | CRI status | `status.reason` | 仅在当前字段为空时写入 |

#### 4.6.3 CGroup 字段

| 字段 | 来源 | 获取方式 | 逻辑 |
|---|---|---|---|
| `CGroupPath` | OCI spec | `spec.Linux.CgroupsPath` | 直接使用 spec 中的 cgroup path |
| `CGroupVersion` | host cgroup fs | `cgroupReader.GetVersion()` | 前提是成功读取过 `CGroupLimits` |
| `CGroupLimits.CPUQuota` | cgroup fs | v1: `cpu.cfs_quota_us`；v2: `cpu.max` 第 1 列 | v2 中 `max` 表示无限制 |
| `CGroupLimits.CPUPeriod` | cgroup fs | v1: `cpu.cfs_period_us`；v2: `cpu.max` 第 2 列 | - |
| `CGroupLimits.CPUShares` | cgroup fs | v1: `cpu.shares`；v2: `cpu.weight` | v2 里以 weight 近似映射到 shares 展示 |
| `CGroupLimits.MemoryLimit` | cgroup fs | v1: `memory.limit_in_bytes`；v2: `memory.max` | 遇到 unlimited 哨兵值或 `max` 则保持 0 |
| `CGroupLimits.MemoryUsage` | cgroup fs | v1: `memory.usage_in_bytes`；v2: `memory.current` | - |
| `CGroupLimits.PidsLimit` | cgroup fs | v1: `pids.max`；v2: `pids.max` | `max` 表示无限制 |
| `CGroupLimits.PidsCurrent` | cgroup fs | `pids.current` | - |
| `CGroupLimits.BlkioWeight` | cgroup fs | v1: `blkio.weight`；v2: `io.weight` 的 `default <num>` | - |

#### 4.6.4 镜像 / snapshot / 挂载字段

| 字段 | 来源 | 获取方式 | 逻辑 |
|---|---|---|---|
| `ImageName` | containerd metadata | `info.Image` | 直接赋值 |
| `ImageConfig` | 未填充 | - | 需要调用 `GetImageConfigInfo()` |
| `ImageLayers` | 未填充 | - | 当前实现把镜像层详情交给 `GetImageLayers()` |
| `SnapshotKey` | containerd metadata | `info.SnapshotKey` | 活跃 RW snapshot key |
| `Snapshotter` | containerd metadata | `info.Snapshotter` | 如 `overlayfs` / `native` |
| `ReadOnlyLayerPath` | 未填充 | - | 当前 detail 里未直接设置，只在 `GetImageLayers()` 中按层解析 |
| `WritableLayerPath` | snapshot API | `SnapshotService(info.Snapshotter).Mounts(ctx, info.SnapshotKey)` 里解析 `upperdir=` | 只对支持 overlay mount 选项的 snapshotter 生效 |
| `Mounts` | CRI + OCI spec + mountinfo | `resolveContainerMounts()` | 详见下方“挂载合并规则” |
| `MountCount` | 运行时计算值 | `len(detail.Mounts)` | 直接计数 |
| `RWLayerSize` | 未填充 | - | 当前实现没有把“内容大小”写回该字段 |
| `RWLayerUsage` | snapshot API | `snapshotter.Usage(ctx, info.SnapshotKey).Size` | 反映实际磁盘占用 |
| `RWLayerInodes` | snapshot API | `snapshotter.Usage(ctx, info.SnapshotKey).Inodes` | - |

#### 4.6.5 Runtime / OCI / shim 字段

| 字段 | 来源 | 获取方式 | 逻辑 |
|---|---|---|---|
| `ShimPID` | procfs / fallback | 优先沿 `taskPID -> ppid` 向上最多 3 层找 `containerd-shim*` 进程；失败则取主进程直接父 PID | 先精确识别 shim，再退化 |
| `OCIBundlePath` | 约定路径 | `filepath.Join("/run/containerd/io.containerd.runtime.v2.task", namespace, containerID)` | 由 `resolveOCIBundleDir()` 推导 |
| `OCIRuntimeDir` | runtime options / 约定路径 | 优先 `runc options.Root`，其次默认 `/run/containerd/runc`，再退化到 bundle convention | 由 `resolveOCIStateDir()` 决定 |
| `Namespaces` | OCI spec | 遍历 `spec.Linux.Namespaces`，保存 `type -> path` | 例如 `network`, `pid`, `mount` |
| `RuntimeProfile` | 多源组合 | `populateRuntimeProfile()` | 详见下一节 |

#### 4.6.6 网络字段

| 字段 | 来源 | 获取方式 | 逻辑 |
|---|---|---|---|
| `IPAddress` | CRI PodSandbox status | `criClient.InspectPodSandboxNetwork(info.SandboxID)` 后取 `PrimaryIP` | 最终直接镜像 `detail.PodNetwork.PrimaryIP` |
| `PortMappings` | CRI sandbox config | `info.config.portMappings` | 通过 `convertCRIPortMappings()` 转成内部模型 |
| `PodNetwork` | CRI + OCI spec + procfs | `populatePodNetwork()` | 聚合 sandbox 网络、namespace、CNI、观察到的接口统计与 warnings |

---

### 4.6.3 `RuntimeProfile` 字段来源

`RuntimeProfile` 是给 UI 的运行时信息视图使用的结构化运行时元数据。

#### `RuntimeProfile.OCI`

| 字段 | 来源 | 获取方式 | 逻辑 |
|---|---|---|---|
| `RuntimeName` | containerd metadata | `info.Runtime.Name` | 例如 `io.containerd.runc.v2` |
| `RuntimeBinary` | runtime options / bundle / procfs / 推导 | 优先 `runcoptions.Options.BinaryName`；其次读取 `bundleDir/shim-binary-path`；再用 shim 进程 `exe`；最后由 runtime name 推导 `containerd-shim-*` | 有明确优先级 |
| `StateDir` | runtime options / 默认路径 / 约定路径 | `resolveOCIStateDir()` | 对 runc 可优先使用 `Options.Root` |
| `BundleDir` | containerd runtime v2 task 约定路径 | `/run/containerd/io.containerd.runtime.v2.task/<ns>/<id>` | 当前固定按 convention 推导 |
| `ConfigPath` | bundle 文件 | `bundleDir/config.json` 存在时写入 | `existingPath()` 先判断存在 |
| `SandboxID` | containerd metadata / bundle 文件 | 先 `info.SandboxID`；若为空，后续在解析 shim socket 时可通过 `bundleDir/sandbox` 回填 | 由 `populateRuntimeProfile()` / `resolveShimSocketAddress()` 联动补齐 |
| `ConfigSource` | 说明字段 | 通常等于 bundle 来源 | 用于说明数据是从 bundle 拿到的 |
| `StateDirSource` | 说明字段 | `runtime-options` / `runtime-default` / `convention` | 标记来源 |
| `BundleDirSource` | 说明字段 | 当前为 `convention` | - |
| `RuntimeSource` | 说明字段 | `containerd` / `runtime-options` / `bundle` / `procfs` / `derived` | 描述 `RuntimeBinary` 的来源 |

#### `RuntimeProfile.Shim`

| 字段 | 来源 | 获取方式 | 逻辑 |
|---|---|---|---|
| `PID` | procfs / fallback | 与 `detail.ShimPID` 同源 | - |
| `BinaryPath` | procfs | shim 进程 `/proc/<ppid>/exe` | 只在成功识别 shim 进程时有值 |
| `SocketAddress` | bundle / sandbox bundle / 推导 | 先读 `bootstrap.json.address`，再读 `address` 文件；如果容器 bundle 没有，则查 sandbox bundle；再退化为对 state path 做 sha256 后拼 `unix:///run/containerd/s/<hash>` | 覆盖 containerd shim socket 的几种常见落点 |
| `Cmdline` | procfs | shim 进程 `/proc/<pid>/cmdline` | 原样保存 argv |
| `BundleDir` | 约定路径 | 与 `OCI.BundleDir` 相同 | - |
| `SandboxBundleDir` | sandbox bundle | 在通过 sandbox ID 解析 socket 时得到 | 仅 sandbox 级 socket 情况有值 |
| `Source` | 说明字段 | `bundle` / `sandbox-bundle` / `procfs` / `inferred` / `convention` | 描述 shim 信息来源 |

#### `RuntimeProfile.CGroup`

| 字段 | 来源 | 获取方式 | 逻辑 |
|---|---|---|---|
| `RelativePath` | OCI spec / procfs fallback | 优先 `detail.CGroupPath`，否则 `procReader.ReadUnifiedCGroupPath(detail.PID)` | “spec 优先，procfs 回填” |
| `AbsolutePath` | 推导值 | `filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(relativePath, "/"))` | 直接拼接绝对路径 |
| `Version` | cgroup reader | `detail.CGroupVersion` | 只有成功读取 limits 后才有可靠值 |
| `Driver` | 推导值 | `inferCGroupDriver(path)` | 路径含 `.slice` 或 `:cri-containerd:` 判定为 `systemd`，否则 `cgroupfs` |
| `Source` | 说明字段 | `spec` 或 `procfs` | 标记来源 |

#### `RuntimeProfile.RootFS`

| 字段 | 来源 | 获取方式 | 逻辑 |
|---|---|---|---|
| `BundleRootFSPath` | bundle 目录 | `bundleDir/rootfs` 存在时写入 | 适合 OCI bundle 直接暴露 rootfs 的场景 |
| `MountRootFSPath` | live mountinfo | 找到容器 `/` 根挂载；若是 overlay，则优先取 `upperdir`；否则取根挂载 `Source` | 由 `resolveMountRootFSPath()` 完成 |
| `Source` | 说明字段 | bundle 来源或 `mountinfo` | 表示 rootfs 路径从哪来 |

---

### 4.6.4 挂载合并规则：`resolveContainerMounts()`

containerd 运行时的挂载不是只看一个来源，而是把以下 3 份数据合并：

1. **CRI mounts**
   - `InspectContainerMounts(ctx, containerID)`
   - 来源于 CRI `ContainerStatus(verbose=true)`
   - 同时读取：
     - `status.mounts`
     - `info.config.mounts`

2. **OCI spec mounts**
   - `spec.Mounts`

3. **live mounts**
   - `/proc/<pid>/mountinfo`

### 合并优先级

1. **CRI config mounts** 为主
2. **CRI status mounts** 用于确认 mount 已 live
3. **spec mounts** 中剩余未被 CRI 认领的条目，作为 runtime default mounts
4. **live mountinfo** 中剩余未匹配项，作为 live residual mounts

### `models.Mount` 字段来源

| 字段 | 来源 | 获取方式 | 逻辑 |
|---|---|---|---|
| `Source` | CRI hostPath / CRI image / live source / spec source | `buildCRIMount()` / `buildRuntimeDefaultMount()` / `buildLiveExtraMount()` | 优先级取决于 mount 类型与来源 |
| `Destination` | CRI `ContainerPath` / spec `Destination` / live `Destination` | 直接赋值 | 比较时会做路径清洗，并把 `/var/run` 规范化为 `/run` |
| `Type` | spec / live / CRI 推断 | `bestMountType()` | spec 优先；live 次之；CRI image-backed 标记为 `image`；否则 `bind` |
| `Options` | spec / live / CRI flags | `bestMountOptions()` | spec 优先；live 次之；CRI 通过 `runtimecri.MountOptions()` 转换成类 OCI 风格 |
| `HostPath` | CRI host path / spec source | CRI mount 直接取 `HostPath`；runtime default mount 取 `spec.Source` | 仅表示主机侧声明路径 |
| `LiveSource` | mountinfo | live mount 的 `Source` | 用来展示实际 live source |
| `Origin` | 合并逻辑 | `cri` / `runtime-default` / `live-extra` | 标记来源类别 |
| `State` | 合并逻辑 | `declared+live` / `declared-only` / `live-only` | 用 live 是否观测到来决定 |
| `Note` | 合并逻辑 | 例如 `CRI external mount`、`runtime default support mount`、`live mountinfo entry outside CRI and spec declarations` | 便于 UI 解释来源 |

---

## 4.7 `ListImages(ctx)`

### 实现路径

1. `r.client.ListImages(ctx)`
2. 对每个 image 构造 `models.Image`
3. 分别读取：
   - `img.Name()`
   - `img.Metadata().CreatedAt`
   - `img.Metadata().Labels`
   - `img.Size(ctx)`（失败则保持 0）
   - `img.Target().Digest`

### `models.Image` 字段来源（ListImages / GetImage 共用）

| 字段 | 来源 | 获取方式 | 逻辑 |
|---|---|---|---|
| `Name` | image metadata | `img.Name()` | 直接赋值 |
| `Digest` | image target descriptor | `img.Target().Digest.String()` | target digest 非空时写入 |
| `Size` | containerd image API | `img.Size(ctx)` | 获取失败时保留 0 |
| `CreatedAt` | image metadata | `img.Metadata().CreatedAt` | 直接赋值 |
| `Labels` | image metadata | `img.Metadata().Labels` | 直接赋值 |
| `Layers` | 未填充 | - | 当前列表/单镜像查询不在这里展开层信息 |

---

## 4.8 `GetImage(ctx, ref)`

### 实现路径

1. `r.client.GetImage(ctx, ref)`
2. 按与 `ListImages()` 相同的规则构造 `models.Image`

### 返回内容特点

- 这是“**单镜像基础视图**”
- 不负责解析 config blob、manifest、diffIDs、snapshot 路径
- 更细粒度信息分别由 `GetImageConfigInfo()` 和 `GetImageLayers()` 提供

---

## 4.9 `GetImageConfigInfo(ctx, imageID)`

### 实现路径

1. 调用 `getImageInfo(ctx, imageID)`，一次性解析：
   - image target descriptor
   - platform 对齐后的 config descriptor
   - 对应 manifest
   - config 中的 `rootfs.diff_ids`

2. `describeImageTarget(info.target.MediaType)` 判断 target 是 manifest 还是 index，并归类为 OCI / Docker schema
3. 构造 `models.ImageConfigInfo`

### `models.ImageConfigInfo` 字段来源

| 字段 | 来源 | 获取方式 | 逻辑 |
|---|---|---|---|
| `Digest` | config descriptor | `info.configDesc.Digest.String()` | 直接赋值 |
| `ContentPath` | content store 路径推导 | `r.getContentPath(info.configDesc.Digest)` | 根据 digest 推导 blob 在本地 content store 的路径 |
| `Size` | config descriptor | `info.configDesc.Size` | 直接赋值 |
| `TargetMediaType` | image target descriptor | `info.target.MediaType` | 可区分 manifest / index |
| `TargetKind` | 推导值 | `describeImageTarget()` | 例如 `manifest` / `index` |
| `Schema` | 推导值 | `describeImageTarget()` | 例如 `OCI` / `Docker` |

---

## 4.10 `GetImageLayers(ctx, imageID, snapshotter, rwSnapshotKey)`

### 实现路径

1. 先 `getImageInfo(ctx, imageID)`，确保 config / manifest / diffIDs 来自同一平台
2. 校验 `manifest.Layers` 与 `diffIDs` 数量一致
3. 进入 `buildImageLayers()`

### `buildImageLayers()` 核心逻辑

1. 如果 `snapshotterName` 为空，默认用 `overlayfs`
2. 通过 `calculateChainIDs(diffIDs)` 从 base 到 top 计算每层 chain ID
3. 如果传入 `rwSnapshotKey`，先通过 `Snapshotter.Mounts(ctx, rwSnapshotKey)` 一次性解析全部 lowerdir，拿到只读层路径
4. 再逐层构造 `models.ImageLayer`
5. 对每层用 `snapshotter.Stat(ctx, chainID)` 判断 snapshot 是否存在
6. 若存在，再用 `snapshotter.Usage(ctx, chainID)` 读取实际磁盘占用与 inode

### `models.ImageLayer` 字段来源

| 字段 | 来源 | 获取方式 | 逻辑 |
|---|---|---|---|
| `Index` | 运行时计算值 | `for i := 0; i < layerCount; i++` | 0 表示 base layer |
| `Label` | 未填充 | - | 当前实现尚未给层写入 `base/mid/top` 标签 |
| `CompressedDigest` | manifest | `manifest.Layers[i].Digest.String()` | 压缩层 digest |
| `UncompressedDigest` | image config | `diffIDs[i].String()` | rootfs diff id |
| `Size` | manifest | `manifest.Layers[i].Size` | 压缩层大小 |
| `CompressionType` | manifest mediaType | `getCompressionType(manifest.Layers[i].MediaType)` | 如 `gzip` / `zstd` |
| `ContentPath` | content store 路径推导 | `r.getContentPath(manifest.Layers[i].Digest)` | 本地 blob 路径 |
| `SnapshotKey` | 运行时计算值 | `chainIDs[i].String()` | layer 对应 chain ID |
| `SnapshotPath` | snapshot mounts | 先从 RW layer mount 的 `lowerdir=` 中取到 `[top...base]`，再按 layer 顺序反向映射 | 只有传入 `rwSnapshotKey` 且 snapshot 已 unpack 时才可能有值 |
| `SnapshotExists` | snapshot API | `snapshotter.Stat(ctx, chainID)` 是否成功 | 表示本地 snapshot 是否存在 |
| `UsageSize` | snapshot API | `snapshotter.Usage(ctx, chainID).Size` | 解压后的实际占用 |
| `UsageInodes` | snapshot API | `snapshotter.Usage(ctx, chainID).Inodes` | inode 使用量 |

---

## 4.11 `ListPods(ctx)`

### 实现路径

1. 先调用 `ListContainers(ctx)`
2. 以 `container.PodUID` 分组
3. 跳过没有 `PodUID` 的容器
4. 为每个分组构造 `models.Pod`

### `models.Pod` 字段来源

| 字段 | 来源 | 获取方式 | 逻辑 |
|---|---|---|---|
| `Name` | 容器标签 | 来自首个容器的 `container.PodName` | 即 `io.kubernetes.pod.name` |
| `Namespace` | 容器标签 | 来自首个容器的 `container.PodNamespace` | 即 `io.kubernetes.pod.namespace` |
| `UID` | 容器标签 | 来自首个容器的 `container.PodUID` | 即 `io.kubernetes.pod.uid` |
| `Containers` | ListContainers 结果 | 把同一 PodUID 的容器收集进切片 | 不额外拷贝字段 |

---

## 4.12 `GetContainerProcesses(ctx, id)`

### 实现路径

1. `loadRunningTask(ctx, id)`：
   - `LoadContainer`
   - `c.Task(ctx, nil)`
   - 如果没有 task，直接返回“container is not running”

2. 调用 `processCollector.CollectContainerProcesses(task.Pid())`

### `CollectContainerProcesses()` 的逻辑

1. 尝试把容器主进程的 rootfs 下 `/proc` 作为容器视角 procfs：

   ```go
   /proc/<containerPID>/root/proc
   ```

2. 列出这个 procfs 里的所有 PID
3. 逐个调用 `ReadProcess(pid)` 读取字段
4. 建立父子关系后返回所有进程
5. 如果容器内 procfs 无法读取，则回退为只读取 host 上的主进程本身

### `models.Process` 字段来源

| 字段 | 来源 | 获取方式 | 逻辑 |
|---|---|---|---|
| `PID` | 调用参数 / procfs | 读取进程时先写入当前 pid | 直接赋值 |
| `PPID` | `/proc/<pid>/stat` | 解析 stat 中 `ppid` 字段 | - |
| `Command` | `/proc/<pid>/cmdline` | argv[0] 取 `filepath.Base()` | 若 cmdline 不可读，则退化为 `"[<pid>]"` |
| `Args` | `/proc/<pid>/cmdline` | argv[1:] | 空则保持 nil |
| `State` | `/proc/<pid>/stat` | stat 的 state 字段 | 如 `R/S/D/Z` |
| `UTime` | `/proc/<pid>/stat` | stat 的 `utime` | 原始 clock ticks |
| `STime` | `/proc/<pid>/stat` | stat 的 `stime` | 原始 clock ticks |
| `CPUPercent` | sampler 推导 | `CalculateProcessRates()` 比较前后两次快照 | 受容器 CPU limit 影响，有限额时归一化到“占已分配 CPU 的百分比” |
| `MemoryPercent` | sampler 推导 | `MemoryRSS / containerMemoryLimit * 100` | 只有已知 memory limit 时才有值 |
| `MemoryRSS` | `/proc/<pid>/status` | `VmRSS` | `kB` 会转成字节 |
| `MemoryVMS` | `/proc/<pid>/status` | `VmSize` | 同样转成字节 |
| `ReadBytes` | `/proc/<pid>/io` | `read_bytes` | 累积值 |
| `WriteBytes` | `/proc/<pid>/io` | `write_bytes` | 累积值 |
| `ReadOps` | `/proc/<pid>/io` | `syscr` | 累积值 |
| `WriteOps` | `/proc/<pid>/io` | `syscw` | 累积值 |
| `ReadBytesPerSec` | sampler 推导 | 与上一次快照比较 | 只有采样间隔足够大时才有值 |
| `WriteBytesPerSec` | sampler 推导 | 与上一次快照比较 | 同上 |
| `Children` | 运行时构建 | 按 `PPID` 建立父子关系 | 只表示当前返回集内的父子关系 |

---

## 4.13 `GetContainerTop(ctx, id)`

### 实现路径

1. 通过 `loadRunningTask(ctx, id)` 获取运行中 task
2. `c.Spec(ctx)` 读取 `spec.Linux.CgroupsPath`
3. 调用 `processCollector.CollectProcessTop(task.Pid(), cgroupPath)`

### `models.ProcessTop` 字段来源

| 字段 | 来源 | 获取方式 | 逻辑 |
|---|---|---|---|
| `Processes` | procfs | 复用 `CollectContainerProcesses()` | 与 `GetContainerProcesses()` 同源 |
| `NetworkIO` | `/proc/<host-pid>/net/dev` | `procReader.ReadNetDev(containerPID)` | 读取 host PID 所在 namespace 的网络统计，再经 sampler 计算速率 |
| `Timestamp` | 本地时间 | `time.Now().Unix()` | 采样时间戳 |
| `CPUCores` | cgroup limits | 若读到 `CPUQuota` 与 `CPUPeriod`，则 `quota / period` | 0 表示未限制或未知 |
| `MemoryLimit` | cgroup limits | `limits.MemoryLimit` | 0 表示未限制或未知 |

### `models.NetworkStats` 字段来源

`NetworkIO` 里的每个元素都来自 `/proc/<pid>/net/dev`：

| 字段 | 来源 | 获取方式 | 逻辑 |
|---|---|---|---|
| `Interface` | `/proc/<pid>/net/dev` | 行首接口名 | 会过滤 `lo`、`veth*`、`virbr*` 及一批内核默认 tunnel 设备 |
| `RxBytes` | `/proc/<pid>/net/dev` | 第 1 列 | 累积值 |
| `TxBytes` | `/proc/<pid>/net/dev` | 第 9 列 | 累积值 |
| `RxPackets` | `/proc/<pid>/net/dev` | 第 2 列 | 累积值 |
| `TxPackets` | `/proc/<pid>/net/dev` | 第 10 列 | 累积值 |
| `RxErrors` | `/proc/<pid>/net/dev` | 第 3 列 | 累积值 |
| `TxErrors` | `/proc/<pid>/net/dev` | 第 11 列 | 累积值 |
| `RxDropped` | `/proc/<pid>/net/dev` | 第 4 列 | 累积值 |
| `TxDropped` | `/proc/<pid>/net/dev` | 第 12 列 | 累积值 |
| `RxBytesPerSec` | sampler 推导 | 与上一次采样对比 | 只有接口持续存在且采样间隔足够大时才有值 |
| `TxBytesPerSec` | sampler 推导 | 与上一次采样对比 | 同上 |

---

## 4.14 `GetContainerMounts(ctx, id)`

### 实现路径

1. `loadRunningTask(ctx, id)` 取得运行中 task
2. `c.Spec(ctx)` 读取 OCI spec
3. 调用 `resolveContainerMounts(ctx, id, spec, task.Pid())`

### 返回内容特点

- 这是“**挂载明细专用接口**”
- 返回值就是前文 `GetContainerRuntimeInfo()` 里写入 `detail.Mounts` 的同一套合并结果
- 所以 `models.Mount` 字段来源与“4.6.4 挂载合并规则”完全一致

---

## 5. `populatePodNetwork()` 字段补全过程

虽然 `PodNetwork` 最终是 `GetContainerRuntimeInfo()` 的一部分，但它的数据源比较复杂，单独列出更容易扩展到其他 runtime。

### 5.1 主来源

1. **containerd metadata**
   - `info.SandboxID`

2. **OCI spec**
   - network namespace path

3. **CRI PodSandboxStatus(verbose=true)**
   - sandbox state
   - primary / additional IPs
   - namespace mode
   - host network
   - runtime handler
   - hostname
   - DNS
   - port mappings
   - runtime type
   - CNI result

4. **procfs**
   - `/proc/<pid>/net/dev` 中真实观察到的接口统计

### 5.2 `models.PodNetworkInfo` 字段来源

| 字段 | 来源 | 获取方式 | 逻辑 |
|---|---|---|---|
| `SandboxID` | containerd metadata | `info.SandboxID` | 若为空，会记录 warning |
| `SandboxState` | CRI status | `status.state` | - |
| `PrimaryIP` | CRI status / metadata | 优先 `status.network.ip`，否则 `info.metadata.ip` | - |
| `AdditionalIPs` | CRI status / metadata | 优先 `status.network.additional_ips`，否则 metadata | - |
| `HostNetwork` | CRI namespace options | `applyNamespaceOptions()` | 根据 network namespace mode 推导 |
| `NamespaceMode` | CRI namespace options | `NamespaceMode` 枚举转文本 | - |
| `NamespaceTargetID` | CRI namespace options | target/container 相关字段 | 由 `applyNamespaceOptions()` 设置 |
| `NetNSPath` | spec / CRI metadata / CRI runtime spec | 先用 detail.Namespaces 中的 network path；若 CRI 返回不同路径会覆盖并记录 warning | 有来源优先级与冲突提示 |
| `Hostname` | CRI sandbox config | `info.config.hostname` | - |
| `DNS` | CRI sandbox config / CNI result | 首先取 config 中的 DNS；CNI 结果单独写到 `CNI.DNS` | 保留两层语义 |
| `PortMappings` | CRI sandbox config | `info.config.portMappings` | 转成内部模型 |
| `RuntimeHandler` | CRI status / metadata | 优先 status，缺失时回退 metadata | - |
| `RuntimeType` | CRI verbose info | `info.runtimeType` | - |
| `StatusSource` | 说明字段 | 固定 `cri-status` | - |
| `ConfigSource` | 说明字段 | 固定 `cri-info` | - |
| `NamespaceSource` | 说明字段 | `containerd-spec` / `cri-status` / `cri-info-metadata` / `cri-info-runtime-spec` | 标记最终采用的 namespace 路径来源 |
| `CNI` | CRI verbose info | `info.cniResult` 归一化后写入 | - |
| `ObservedInterfaces` | procfs | `procReader.ReadNetDev(detail.PID)` | 真实观测到的接口流量 |
| `Warnings` | 运行时补充 | 例如 sandbox id 缺失、CRI 请求失败、netns path mismatch、procfs 读取失败 | 供 UI 呈现“尽力而为”结果 |

---

## 6. 当前 containerd 实现中明确“未填充/仅部分填充”的字段

为了避免后续 runtime 实现误以为这些字段已经有统一语义，下面显式列出当前还没有完全实现的部分：

| 模型字段 | 当前状态 | 说明 |
|---|---|---|
| `models.Container.ImageID` | 未填充 | 目前基础容器视图不单独解析 image digest/id |
| `models.Container.StartedAt` | 仅 `GetContainerRuntimeInfo()` 会尝试通过 CRI status 填充 | `ListContainers()` / `GetContainer()` / `GetContainerDetail()` 不填 |
| `models.ContainerDetail.Processes` | 未填充 | 进程列表通过 `GetContainerProcesses()` 返回 |
| `models.ContainerDetail.ImageConfig` | 未填充 | 由 `GetImageConfigInfo()` 单独提供 |
| `models.ContainerDetail.ImageLayers` | 未填充 | 层详情由 `GetImageLayers()` 单独提供 |
| `models.ContainerDetail.ReadOnlyLayerPath` | 未填充 | 只读层路径目前在 `GetImageLayers()` 中逐层给出 |
| `models.ContainerDetail.RWLayerSize` | 未填充 | 当前只记录 `RWLayerUsage` / `RWLayerInodes` |
| `models.Image.Layers` | 未填充 | 列表/单镜像基础视图未展开层数据 |
| `models.ImageLayer.Label` | 未填充 | 当前没有写入 `base/mid/top` |

这些字段未来如果要补齐，建议仍然遵守本文档中的原则：**优先写清楚来源、优先级和 fallback，再补代码**。

---

## 7. 给未来运行时实现的落地建议

如果未来增加 `CRI-O`、docker 或其他 runtime，建议保持下面的实现顺序：

1. **先保证基础容器/镜像/Pod 列表可用**
   - `ListContainers`
   - `GetContainer`
   - `ListImages`
   - `ListPods`

2. **再补运行时详情**
   - `GetContainerDetail`
   - `GetContainerRuntimeInfo`

3. **最后补实时/宿主机依赖强的能力**
   - `GetContainerProcesses`
   - `GetContainerTop`
   - `GetContainerMounts`

4. **文档同步要求**
   - 为新 runtime 增加一个与“第 3~6 节”同结构的小节
   - 每个返回模型都要有字段映射表
   - 明确哪些字段来自 runtime 原生 API，哪些来自 CRI / OCI / procfs / cgroup / 推导
   - 明确哪些字段暂未填充

这样可以保证：

- UI 层知道每个字段的可信来源
- 后续新增运行时不会破坏现有字段语义
- 出现字段缺失时，能快速定位是“源头没有”还是“当前实现没补”

---

## 8. 规划中的 `CRI-O` 实现：按 `CRI-O` 自身语义组织的字段与数据流

> 本节描述的是**未来新增 `CRI-O` 运行时实现时建议采用的解析路径**，不是当前仓库已经存在的实现。  
> 重点不是把 containerd 的术语强行套到 `CRI-O` 上，而是按 `CRI-O` 自己的运行时分层来定义：**CRI API、`containers/image`、`containers/storage`、`conmon`、OCI runtime、procfs/mountinfo/cgroup**。

### 8.1 设计原则：不要用 containerd 概念反推 `CRI-O`

规划 `CRI-O` 实现时，建议先明确下面 4 条原则：

1. **容器/Pod 生命周期优先看 CRI**
   - `CRI-O` 对 kubelet 暴露的是标准 CRI。
   - 容器列表、PodSandbox、状态、挂载、网络、生命周期字段，首先应来自 CRI，而不是先假设某个内部 state 目录结构。

2. **镜像与层信息优先看 containers 生态**
   - `CRI-O` 的镜像与本地存储不应继续用 containerd 的 `content store`、`snapshotter`、`chainID` 去解释。
   - 规划时应优先考虑：
     - `go.podman.io/image/v5`（原 `containers/image`）
     - `go.podman.io/storage`（原 `containers/storage`）

3. **监控进程应明确写成 `conmon`**
   - `conmon` 是 `CRI-O` / Podman 体系里的容器 monitor 和通信进程。
   - 它不是 containerd runtime v2 里的 “shim”；文档里不建议继续把这层概念写成 `shim` 语义。

4. **允许为 `CRI-O` 扩展 runtime-specific 字段**
   - 当前 `pkg/models` 里已有一些明显偏 containerd 的命名，例如：
     - `SnapshotKey`
     - `Snapshotter`
     - `ImageConfigInfo.ContentPath`
     - `RuntimeProfile.Shim`
   - `CRI-O` 实现不应为了兼容这些名字而歪曲数据来源。
   - 如果落地时发现信息结构确实不同，建议新增 `CRI-O` 专属字段或子结构，而不是继续硬套 containerd 术语。

### 8.2 `CRI-O` 的建议数据源与可借助库

建议把 `CRI-O` 实现的数据源分成 6 层：

| 层级 | 主要内容 | 推荐入口/库 | 作用 |
|---|---|---|---|
| CRI Runtime | 容器、PodSandbox、状态、挂载、网络 | `k8s.io/cri-api` + gRPC；仓库现有 `pkg/runtime/cri` | 主入口，负责 kubelet 视角的运行时信息 |
| CRI Image | 镜像列表与镜像状态 | `k8s.io/cri-api` + gRPC | 镜像基础信息 |
| CRI-O 配置 | `crio.conf`、运行时选择、默认挂载、cgroup driver | 配置文件解析；可结合 containers 生态配置库 | 解释默认行为与路径约定 |
| Image 库 | 镜像引用、manifest、config、digest/media type | `go.podman.io/image/v5`（原 `containers/image`） | 补 image config、manifest、registry/ref 语义 |
| Storage 库 | 本地 layer/image/container 存储元数据 | `go.podman.io/storage`（原 `containers/storage`） | 补 layer 拓扑、rw layer、usage、driver 信息 |
| 宿主机观测 | `conmon`、主进程、mount、cgroup、net/dev | `pkg/sysinfo`、`/proc`、`/sys/fs/cgroup`、mountinfo | 补 live 状态与宿主机视图 |

### 8.3 建议的 `CrioRuntime` 依赖结构

如果后续新增 `pkg/runtime/crio`，建议结构体依赖直接按 `CRI-O` 的分层来设计：

| 字段 | 作用 | 建议实现/库 | 备注 |
|---|---|---|---|
| `config *runtime.Config` | 保存 socket / timeout 等配置 | 本仓库 `runtime.Config` | `Namespace` 对 `CRI-O` 不是核心语义，可弱化 |
| `runtimeClient` | CRI RuntimeService client | `k8s.io/cri-api` | 取容器、Pod、状态、挂载、网络 |
| `imageClient` | CRI ImageService client | `k8s.io/cri-api` | 取镜像列表与镜像状态 |
| `imageInspector` | 读镜像 config/manifest/ref | `go.podman.io/image/v5` | 比继续写 “content store” 更贴近 `CRI-O` |
| `storageStore` | 读本地镜像/容器/layer 存储 | `go.podman.io/storage` | 用于 layer、rw layer、driver、usage |
| `procReader` | 读取 `/proc` | `pkg/sysinfo` | 识别主进程、`conmon`、net/dev、cwd、exe |
| `cgroupReader` | 读取 cgroup | `pkg/sysinfo` | limits / usage / cgroup version |
| `mountReader` | 读取 mountinfo | `pkg/sysinfo` | live mounts、rootfs、upperdir |
| `processCollector` | top/进程/网络速率 | `pkg/sysinfo` | 可直接复用当前实现 |
| `configReader`（可选） | 读取 `crio.conf` / `storage.conf` | 本地解析器或 containers 生态配置库 | 用于解释 root/runroot/default mounts |

### 8.4 `CRI-O` 数据获取总流程

建议把 `CRI-O` 运行时实现理解为下面这条链路：

1. **先连 CRI socket**
   - 默认优先读 `runtime.Config.SocketPath`
   - 若未配置，再读 `crio.conf` 的 `listen`
   - 最后回退到：

```
/var/run/crio/crio.sock
```

2. **先拿 kubelet 视角的数据**
   - `ListContainers`
   - `ContainerStatus(verbose=true)`
   - `ListPodSandbox`
   - `PodSandboxStatus(verbose=true)`
   - `ListImages`
   - `ImageStatus(verbose=true)`

3. **再拿 `CRI-O` 本地补充数据**
   - `crio.conf`
   - `storage.conf`
   - `go.podman.io/image/v5` 的 image/config/manifest 信息
   - `go.podman.io/storage` 的 layer/container/image/store 信息

4. **最后叠加宿主机 live 观测**
   - `/proc/<pid>/...`
   - `/proc/<pid>/mountinfo`
   - `/sys/fs/cgroup/...`
   - `conmon` 进程信息

文档中建议始终把字段来源写成：

1. **CRI**
2. **containers 库**
3. **host observation**
4. **inferred**

不要再把 `CRI-O` 的说明组织成 “metadata → content → snapshot → shim”。

### 8.5 连接、配置与默认行为字段

`CRI-O` 的很多“为什么会这样”字段，不来自某个 container object，而来自配置：

| 配置项 | 常见默认值 | 说明 |
|---|---|---|
| `listen` | `/var/run/crio/crio.sock` | CRI gRPC socket |
| `root` | `/var/lib/containers/storage` | 持久化镜像/容器存储根目录 |
| `runroot` | `/var/run/containers/storage` | 运行态存储根目录 |
| `default_runtime` | 常见为 `crun` | 默认 OCI runtime |
| `cgroup_manager` | 常见为 `systemd` | cgroup driver |
| `default_mounts_file` | 依发行版/配置而定 | 默认挂载来源 |
| `log_dir` | 常见为 `/var/log/crio/pods` | 容器日志目录语义 |

这些配置不应该被解释为 containerd 的 runtime dir/snapshotter 配置；它们是 `CRI-O` 自己的行为来源。

### 8.6 `models.Container`：建议保留的公共字段

`models.Container` 这类“基础视图”字段，可以继续共用当前模型，但来源应明确改成 `CRI-O` 语义：

| 字段 | 建议来源 | 获取路径 | 说明 |
|---|---|---|---|
| `ID` | CRI `ListContainers` | `Container.Id` | 直接使用 CRI container id |
| `Name` | CRI labels / metadata | `io.kubernetes.container.name` → `metadata.name` → 截断 id | 与当前 UI 体验保持一致 |
| `Image` | CRI | `Container.Image.Image` 或 `status.Image.Image` | 用户可读引用 |
| `ImageID` | CRI | `Container.ImageRef` / `status.ImageRef` | 对 `CRI-O` 来说应是强字段 |
| `Status` | CRI | `Container.State` / `ContainerStatus.State` | 统一映射到内部状态 |
| `CreatedAt` | CRI | `Container.CreatedAt` | - |
| `StartedAt` | CRI status | `status.StartedAt` | 建议详情接口填充 |
| `PID` | CRI verbose status + host 观测 | `status.Info` 或 pidfile / procfs | 允许多源回退 |
| `PodName` / `PodNamespace` / `PodUID` | CRI labels | Kubernetes labels | 与当前 containerd 实现一致 |
| `Labels` | CRI | `Container.Labels` / `status.Labels` | 透传 |

### 8.7 `models.ContainerDetail`：建议拆成 5 组来解释

#### 8.7.1 生命周期与进程字段

| 字段 | 建议来源 | 说明 |
|---|---|---|
| `ProcessCount` | `/proc/<pid>/root/proc` | 与现有采集方式一致 |
| `Processes` | `GetContainerProcesses()` | 单独接口返回完整进程列表 |
| `Environment` | CRI status verbose / OCI config | `CRI-O` 下 CRI 配置通常应作为主来源 |
| `SharedPID` | CRI namespace options | 必要时用 OCI config 校验 |
| `RestartCount` | CRI status metadata | `Attempt` |
| `ExitedAt` | CRI status | `FinishedAt` |
| `ExitCode` | CRI status | `ExitCode` |
| `ExitReason` | CRI status | `Reason` |

#### 8.7.2 cgroup 与资源字段

`CRI-O` 下建议用下面顺序取 cgroup：

1. CRI verbose status / runtime info
2. OCI `config.json`
3. `/proc/<pid>/cgroup`
4. cgroup fs

| 字段 | 建议来源 | 说明 |
|---|---|---|
| `CGroupPath` | CRI / OCI / procfs | 记录最终采用的 source |
| `CGroupVersion` | `CGroupReader.GetVersion()` | 可直接复用 |
| `CGroupLimits.*` | cgroup fs | 直接复用现有 reader |

#### 8.7.3 挂载与 rootfs 字段

对 `CRI-O` 而言，挂载与 rootfs 建议按下面顺序：

1. CRI config mounts
2. CRI status mounts
3. `default_mounts_file` 和 `CRI-O` 自动注入挂载
4. live mountinfo

| 字段 | 建议来源 | 说明 |
|---|---|---|
| `Mounts` | 上述 4 路合并 | 继续输出统一 `Origin/State/Note` |
| `MountCount` | 计算值 | `len(mounts)` |
| `WritableLayerPath` | live root mount + storage store | 优先信任 live mountinfo |
| `ReadOnlyLayerPath` | storage store / live mount | 取决于 driver 与 layer 挂载方式 |
| `RWLayerUsage` / `RWLayerInodes` | `go.podman.io/storage` + fs 统计 | 不必再表述成 snapshot usage |

#### 8.7.4 网络字段

| 字段 | 建议来源 | 说明 |
|---|---|---|
| `IPAddress` | `PodSandboxStatus(verbose=true)` | 以 sandbox 为准 |
| `PortMappings` | CRI sandbox config/status | Pod 维度字段 |
| `PodNetwork` | CRI + CNI result + `/proc/<pid>/net/dev` | 建议继续复用当前 `PodNetworkInfo` 结构 |

#### 8.7.5 运行时字段

这一组是本次整理里最需要“去 containerd 化”的部分。

当前模型里的这些字段：

- `ShimPID`
- `OCIBundlePath`
- `OCIRuntimeDir`
- `Snapshotter`
- `SnapshotKey`
- `RuntimeProfile.Shim`

都带有明显的 containerd 偏向。对于 `CRI-O`，建议处理方式如下：

| 现有字段 | 对 `CRI-O` 的建议 |
|---|---|
| `ShimPID` | 可临时复用为 `conmon` PID 的兼容出口，但文档应明确它表示的是 `conmon`，不是 shim |
| `OCIBundlePath` | 可以继续保留，表示 OCI bundle 目录 |
| `OCIRuntimeDir` | 可以继续保留，但应解释为 `CRI-O` runtime state dir / OCI runtime work dir |
| `Snapshotter` | 建议忽略；若需要展示存储后端，应扩展为 `StorageDriver` |
| `SnapshotKey` | 建议忽略；若需要唯一标识 rw layer，应扩展为 `ContainerLayerID` / `RWLayerID` |
| `RuntimeProfile.Shim` | 建议不要再按 shim 语义写文档；若模型允许调整，建议扩展为 `Monitor` / `Conmon` 子结构 |

### 8.8 `CRI-O` 建议新增或扩展的 runtime-specific 字段

为了避免继续把 `CRI-O` 解释成 containerd，建议在真正实现时允许增加 `CRI-O` 专属字段。下面是更贴近 `CRI-O` 的命名方向：

| 建议字段/结构 | 含义 | 推荐来源 |
|---|---|---|
| `ConmonPID` | `conmon` 进程 PID | procfs / runtime state |
| `ConmonBinary` | `conmon` 可执行路径 | `/proc/<pid>/exe` |
| `ConmonCmdline` | `conmon` 启动参数 | `/proc/<pid>/cmdline` |
| `ConmonAttachSocket` | attach socket 地址 | runtime state / config |
| `ConmonLogPath` | 容器日志文件或 pod log 路径 | `log_dir` + CRI/container state |
| `StorageDriver` | 当前 `CRI-O` 使用的 storage driver | `storage.conf` / store |
| `StoreImageID` | storage 层 image id | `go.podman.io/storage` |
| `TopLayerID` | image 顶层 layer id | `go.podman.io/storage` |
| `ContainerLayerID` | 容器可写层 id | `go.podman.io/storage` |
| `ManifestDigest` | manifest digest | `go.podman.io/image/v5` |
| `ConfigDigest` | config digest | `go.podman.io/image/v5` |

如果这些字段最终需要展示在 UI 中，建议把它们视作 **`CRI-O` runtime-specific profile**，而不是继续塞回 snapshot/shim 命名下。

### 8.9 镜像与层：按 `containers/image` + `containers/storage` 解释

#### `models.Image`

基础镜像字段仍建议以 CRI ImageService 为主：

| 字段 | 主来源 | 补充来源 |
|---|---|---|
| `Name` | CRI repo tags/digests | `go.podman.io/image/v5` |
| `Digest` | CRI image id / repo digest | image manifest/config |
| `Size` | CRI image status | storage usage |
| `CreatedAt` | image inspect / config metadata | 若 CRI 不提供则从 image 库补 |
| `Labels` | image config labels | image inspect |
| `Layers` | image inspect + storage store | 不必再写成 containerd layer digests 语义 |

#### `models.ImageConfigInfo`

`ImageConfigInfo` 对 `CRI-O` 的解释建议改成：

| 字段 | 对 `CRI-O` 的建议解释 |
|---|---|
| `Digest` | image config digest |
| `ContentPath` | 若继续保留，表示本地 image/config blob 路径，而不是 containerd content store 路径 |
| `Size` | config blob size |
| `TargetMediaType` | manifest / config media type |
| `TargetKind` | manifest kind / schema kind |
| `Schema` | OCI / Docker schema |

这里的数据获取更适合经 `go.podman.io/image/v5`，而不是继续写 “content store lookup”。

#### `models.ImageLayer`

这是最不建议继续照搬 containerd 语义的模型之一。对 `CRI-O` 而言，层信息更适合解释成：

| 字段 | 对 `CRI-O` 的建议解释 |
|---|---|
| `CompressedDigest` | manifest layer digest |
| `UncompressedDigest` | diff id |
| `Size` | manifest size |
| `CompressionType` | layer compression |
| `ContentPath` | layer/blob 在本地 store 中的实际路径 |
| `SnapshotKey` | 建议忽略或重释为 layer id；不要继续称为 snapshot key |
| `SnapshotPath` | 建议忽略或重释为 mounted layer / cache path |
| `SnapshotExists` | 建议忽略或重释为 layer locally present |
| `UsageSize` / `UsageInodes` | layer 实际磁盘占用 |

如果后续发现 `ImageLayer` 模型和 `CRI-O` 语义偏差过大，建议在实现前先调整模型，而不是在实现里不断加注释解释“它其实不是 snapshot”。

### 8.10 `conmon`：建议单独写清楚的数据流

`conmon` 在 `CRI-O` 里承担的是：

- 容器 monitor
- attach socket 中转
- stdout/stderr 日志落盘或转 journald
- 退出码/退出时间记录

因此对 `conmon` 的字段建议优先从下面来源获取：

| 字段 | 建议来源 |
|---|---|
| PID | CRI verbose info / runtime state / 进程树回溯 |
| BinaryPath | `/proc/<pid>/exe` |
| Cmdline | `/proc/<pid>/cmdline` |
| Attach socket | `container-attach-socket-dir` / runtime state |
| Log path | `log_dir` + container/pod 目录布局 |
| Exit file / exit metadata | CRI state / runtime state / `conmon` 维护的退出记录 |

文档中建议把这一层直接写为 **Conmon / Monitor**，不要再叫 Shim。

### 8.11 `ListPods()` / `GetContainerProcesses()` / `GetContainerTop()` / `GetContainerMounts()`

#### `ListPods()`

- 优先走 `ListPodSandbox`
- 以 sandbox metadata 为 Pod 主信息
- 容器与 sandbox 的归属关系也优先按 CRI 建模

#### `GetContainerProcesses()`

- 与当前实现一样，优先从 `/proc/<pid>/root/proc` 取容器内进程
- 不能读取时回退为主进程观测

#### `GetContainerTop()`

- 复用 `ProcessCollector`
- CPU / memory 仍来自 cgroup
- 网络仍来自 `/proc/<host-pid>/net/dev`

#### `GetContainerMounts()`

- 以 CRI mounts 为主
- 再叠加 `default_mounts_file`
- 最后补 live mountinfo

这部分建议继续保持当前 UI 使用的 `MountOrigin` / `MountState` 语义，不需要因为运行时变化而改 UI。

### 8.12 `CRI-O` 实现最值得优先落地的字段

按实现收益排序，建议优先做下面 3 组：

1. **CRI 强字段**
   - `ID`
   - `Name`
   - `Image`
   - `ImageID`
   - `Status`
   - `CreatedAt`
   - `StartedAt`
   - `RestartCount`
   - `ExitCode`

2. **主机观测强字段**
   - `PID`
   - `CGroupPath`
   - `Mounts`
   - `WritableLayerPath`
   - `IPAddress`
   - `PodNetwork`

3. **`CRI-O` 专属增强字段**
   - `ConmonPID`
   - `StorageDriver`
   - `ContainerLayerID`
   - `ManifestDigest`
   - `ConfigDigest`

这样可以先交付稳定的通用视图，再逐步补 `CRI-O` 特色信息。

### 8.13 `CRI-O` 实现前建议先确认的 6 个问题

1. **是否允许扩展 `pkg/models`**
   - 如果允许，建议新增 `Conmon` / `Storage` / `CRI-O profile` 字段
   - 如果不允许，需明确哪些字段只做兼容映射

2. **镜像信息以 CRI 为主还是以 `go.podman.io/image/v5` 为主**
   - 建议：列表用 CRI，细节用 image 库

3. **layer/rw layer 信息以 `go.podman.io/storage` 为主是否可接受**
   - 建议：是

4. **`SnapshotKey` / `Snapshotter` / `Shim` 这些字段是否要继续保留**
   - 建议：文档上弱化；模型上视兼容需求决定

5. **`conmon` 元数据希望展示到什么粒度**
   - 只要 PID
   - 还是还要 attach socket、日志路径、命令行

6. **默认挂载的口径**
   - 只记录 `default_mounts_file`
   - 还是把 `CRI-O` 自动注入的支持性挂载也统一解释成 runtime defaults

把这些问题先定下来，后续实现 `pkg/runtime/crio` 时就不容易被 containerd 术语牵着走。
