# c-ray - Containerd Runtime Implementation

## 实现的功能

### 1. 客户端连接
- `Connect()`: 连接到 containerd socket
- `Close()`: 关闭连接
- 支持自定义 namespace

### 2. 容器管理
- `ListContainers()`: 获取所有容器列表
  - 提取容器基础信息 (ID, Name, Image, Status, PID)
  - 从 labels 提取 Pod 信息 (io.kubernetes.*)
  - 获取容器运行状态和 PID
- `GetContainer()`: 获取单个容器信息
- `GetContainerDetail()`: 获取容器概览与进程摘要相关信息
  - OCI Spec 中与概览直接相关的信息（namespace、环境变量、SharedPID、CGroup 路径）
  - CGroup limits / version
  - 进程数量
  - CRI 状态补充（重启次数、退出状态）
- `GetContainerRuntimeInfo()`: 获取 runtime / shim / OCI 元数据
- `GetContainerStorageInfo()`: 获取 snapshot、rootfs、挂载与 RW layer 信息
- `GetContainerNetworkInfo()`: 获取 sandbox、netns、CNI/CRI 网络信息

### 3. 镜像管理
- `ListImages()`: 获取所有镜像列表
  - 镜像名称、摘要、大小、创建时间
- `GetImage()`: 获取单个镜像信息
- `GetImageLayers()`: 获取镜像层信息
  - 使用 content store 读取 manifest
  - 提取每层的 digest 和 size

### 4. Pod 管理
- `ListPods()`: 从容器 labels 提取 Pod 信息
  - 按 Pod UID 分组容器
  - 支持 Kubernetes 标准 labels:
    - io.kubernetes.pod.name
    - io.kubernetes.pod.namespace
    - io.kubernetes.pod.uid

### 5. 状态转换
- `convertStatus()`: 将 containerd 状态转换为内部模型
  - created, running, paused, stopped, unknown

## Kubernetes Labels 支持

支持的标准 Kubernetes labels:
- `io.kubernetes.container.name`: 容器名称
- `io.kubernetes.pod.name`: Pod 名称
- `io.kubernetes.pod.namespace`: Pod 命名空间
- `io.kubernetes.pod.uid`: Pod UID

## 待实现功能 (Phase 3)

以下功能需要 procfs 和 cgroup 解析支持:
- `GetContainerProcesses()`: 获取容器内进程列表
- `GetContainerTop()`: 获取进程 Top 信息
- `GetContainerMounts()`: 完整的挂载信息解析

## API 使用示例

```go
// 创建运行时
config := &runtime.Config{
    SocketPath: "/run/containerd/containerd.sock",
    Namespace:  "k8s.io",
    Timeout:    30,
}
rt := containerd.NewContainerdRuntime(config)

// 连接
ctx := context.Background()
if err := rt.Connect(ctx); err != nil {
    log.Fatal(err)
}
defer rt.Close()

// 列出容器
containers, err := rt.ListContainers(ctx)
if err != nil {
    log.Fatal(err)
}

for _, c := range containers {
    fmt.Printf("Container: %s (%s) - Status: %s\n",
        c.Name, c.ID[:12], c.Status)
}

// 获取容器概览
detail, err := rt.GetContainerDetail(ctx, containerID)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("PID: %d\n", detail.PID)
fmt.Printf("CGroup: %s\n", detail.CGroupPath)

// 获取存储信息
storageDetail, err := rt.GetContainerStorageInfo(ctx, containerID)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Mounts: %d\n", storageDetail.MountCount)

// 列出镜像
images, err := rt.ListImages(ctx)
if err != nil {
    log.Fatal(err)
}

for _, img := range images {
    fmt.Printf("Image: %s - Size: %d\n", img.Name, img.Size)
}

// 列出 Pods
pods, err := rt.ListPods(ctx)
if err != nil {
    log.Fatal(err)
}

for _, pod := range pods {
    fmt.Printf("Pod: %s/%s - Containers: %d\n",
        pod.Namespace, pod.Name, len(pod.Containers))
}
```

## 测试

运行测试:
```bash
go test ./pkg/runtime/containerd/ -v
```

测试覆盖:
- 运行时创建
- 状态转换
- 错误处理 (未连接时)
- 客户端关闭

注意: 完整的集成测试需要 containerd 运行。

## 技术细节

### Containerd API 版本
使用 containerd v2.2.1 API

### 关键依赖
- `github.com/containerd/containerd/v2/client`: 客户端库
- `github.com/containerd/containerd/v2/core/images`: 镜像操作
- `github.com/opencontainers/image-spec`: OCI 镜像规范

### 错误处理
- 所有方法在未连接时返回错误
- 使用 fmt.Errorf 包装错误提供上下文
- 容器列表获取时跳过单个容器错误，继续处理其他容器

### 性能考虑
- 容器列表预分配切片容量
- 避免不必要的 API 调用
- 错误时快速失败
