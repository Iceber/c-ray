# c-ray

一个基于 containerd 的容器管理命令行 TUI 工具。

## 功能特性

- 📦 容器列表查看（ID、名称、镜像、状态、运行时间）
- 🖼️ 镜像列表管理
- 🎯 Pod 信息展示
- 📊 容器详情页面
  - 进程 Top 视图
  - 进程树展示
  - 挂载卷浏览
  - 镜像层查看
  - 运行时信息

## 快速开始

### 前置要求

- Go 1.24.3+
- containerd 运行中

### 安装

```bash
# 本地构建 (macOS/Linux)
make build

# 为 Linux 交叉编译 (用于 kind/Docker 测试)
GOOS=linux GOARCH=arm64 go build -o bin/cray-linux ./cmd/cray
```

### 测试功能

当前支持命令行测试模式：

```bash
# 列出容器
./bin/cray test list-containers

# 列出镜像
./bin/cray test list-images

# 列出 Pods
./bin/cray test list-pods

# 容器详情
./bin/cray test container-detail <container-id>
```

### 在 kind 集群中测试

```bash
# 使用测试脚本
./scripts/test-in-kind.sh

# 或手动测试
# 1. 为 Linux 构建二进制文件
GOOS=linux GOARCH=arm64 go build -o bin/cray-linux ./cmd/cray

# 2. 复制到 kind-control-plane 容器
cat bin/cray-linux | docker exec -i kind-control-plane bash -c "cat > /usr/local/bin/cray && chmod +x /usr/local/bin/cray"

# 3. 运行测试
docker exec kind-control-plane cray test list-containers
```

### 运行 TUI

```bash
# 使用默认配置
./bin/cray tui

# 自定义 containerd socket 路径
CONTAINERD_SOCKET=/run/containerd/containerd.sock ./bin/cray tui

# 自定义 namespace
CONTAINERD_NAMESPACE=default ./bin/cray tui
```

### 快捷键

- `q` / `Ctrl+C`: 退出应用
- 更多快捷键开发中...

## 项目结构

```
.
├── cmd/
│   └── cray/               # 主程序入口
├── pkg/
│   ├── models/             # 数据模型
│   ├── runtime/            # 运行时抽象接口
│   │   └── containerd/     # containerd 实现
│   ├── sysinfo/            # 系统信息采集
│   └── ui/                 # TUI 界面
│       ├── views/          # 视图组件
│       └── components/     # 可复用组件
├── docs/                   # 文档
├── task_plan.md            # 开发计划
├── findings.md             # 技术研究
└── progress.md             # 进度记录
```

## 开发状态

当前进度: Phase 1 完成 ✅

- [x] 项目架构设计
- [x] 基础框架搭建
- [x] 运行时接口定义
- [x] 数据模型定义
- [ ] Containerd 运行时实现
- [ ] 系统信息采集
- [ ] UI 视图开发

详见 [task_plan.md](task_plan.md)

## 技术栈

- **语言**: Go 1.24.3+
- **TUI 框架**: [tview](https://github.com/rivo/tview)
- **容器运行时**: [containerd](https://github.com/containerd/containerd)
- **终端库**: [tcell](https://github.com/gdamore/tcell)

## 架构设计

### 运行时抽象

项目采用接口抽象设计，支持未来扩展到 cri-o、docker 等运行时：

```go
type Runtime interface {
    ListContainers(ctx context.Context) ([]*models.Container, error)
    GetContainerDetail(ctx context.Context, id string) (*models.ContainerDetail, error)
    // ...
}
```

### 模块划分

- **runtime**: 容器运行时交互层
- **models**: 数据模型定义
- **sysinfo**: 系统信息采集（procfs, cgroup, mount）
- **ui**: TUI 界面层

## 贡献

欢迎提交 Issue 和 Pull Request！

## 许可证

MIT License
