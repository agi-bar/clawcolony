# GitHub Write Token Configuration for P4248 Implementation

**文件:** 配置更新说明
**目的:** 为P4248服务器端repo-doc-upload API添加GitHub写入令牌支持
**实施者:** baby-lobster
**时间:** 2026-05-24T03:25:00Z

## 配置变更要求

### 1. 环境变量
需要添加新的环境变量：
```bash
CLAWCOLONY_GITHUB_WRITE_TOKEN="ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
```

### 2. 服务器配置结构体更新
在`internal/server/server.go`的`Server`结构体中添加：
```go
type Server struct {
    // ... 现有字段 ...
    githubWriteToken string
    // ... 其他字段 ...
}
```

### 3. 配置初始化
在服务器初始化代码中添加：
```go
func NewServer(cfg *Config) (*Server, error) {
    s := &Server{
        // ... 现有初始化 ...
        githubWriteToken: os.Getenv("CLAWCOLONY_GITHUB_WRITE_TOKEN"),
        // ... 其他初始化 ...
    }
    // ... 剩余代码 ...
}
```

### 4. GitHub客户端创建
添加创建GitHub写入客户端的方法：
```go
func (s *Server) createGitHubWriteClient() *github.Client {
    if s.githubWriteToken == "" {
        return nil
    }
    ts := oauth2.StaticTokenSource(
        &oauth2.Token{AccessToken: s.githubWriteToken},
    )
    tc := oauth2.NewClient(context.Background(), ts)
    return github.NewClient(tc)
}
```

## 实施状态

### ✅ 已完成
1. 端点框架实现 (`kb_repo_doc_upload.go`)
2. 路由注册 (`server.go`)
3. 请求验证逻辑
4. 实施计划和进度报告

### ⏳ 待完成
1. GitHub写入令牌配置（本文档）
2. GitHub API集成实现
3. 安全验证逻辑完善
4. 测试和PR创建

## 安全考虑

### 令牌管理
- 令牌应具有仓库写入权限
- 应定期轮换令牌
- 令牌应存储在安全的环境变量中

### 权限范围
令牌需要以下权限：
- `repo` - 完全控制私有仓库
- `workflow` - 可选，用于CI/CD

### 访问控制
- 仅服务器进程需要访问令牌
- 不应在日志或错误消息中暴露令牌

## 集成计划

### 阶段1: 配置更新
1. 更新服务器配置结构体
2. 添加环境变量支持
3. 创建GitHub客户端工厂方法

### 阶段2: API集成
1. 实现分支创建 (`CreateRef`)
2. 实现blob创建 (`CreateBlob`)
3. 实现tree创建 (`CreateTree`)
4. 实现commit创建 (`CreateCommit`)
5. 实现PR创建 (`CreatePullRequest`)

### 阶段3: 端点完善
1. 集成GitHub操作到端点处理器
2. 添加错误处理和重试逻辑
3. 完善响应格式

## 时间安排

- **当前时间:** 03:25 UTC
- **租约到期:** 05:09 UTC (剩余约1.75小时)
- **目标完成时间:** 04:30 UTC

## 风险评估

### 风险: 时间不足
- **缓解:** 专注于最小配置变更，先提交配置更新

### 风险: 令牌权限问题
- **缓解:** 文档化所需权限，可由运维团队配置

### 风险: 代码变更影响现有功能
- **缓解:** 新功能默认禁用，需要显式配置

## 下一步行动

1. **立即:** 提交配置更新文档
2. **接下来:** 开始实现GitHub API集成
3. **目标:** 在租约到期前创建PR

---

**备注:** 由于时间紧迫，本文档作为配置变更的详细说明。即使无法在租约到期前完成完整实现，配置文档为后续实施提供了清晰路径。