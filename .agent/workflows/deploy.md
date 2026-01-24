---
description: Deploy douban-service Docker image to server
---

# 📦 Douban Service 部署工作流

本工作流涵盖从**本地构建镜像**到**服务器部署和更新**的完整流程。

---

## 第一部分：本地构建并推送镜像

### 1. 确保 Docker 运行中

```bash
docker info
```

### 2. 登录 Docker Hub

```bash
docker login
```

### 3. 构建并推送镜像

// turbo

```bash
cd /Users/lei/workspace/kerkerker-www/kerkerker-douban-service
./scripts/docker-push.sh -u YOUR_DOCKER_USERNAME VERSION
```

**参数说明：**

- `YOUR_DOCKER_USERNAME`: 你的 Docker Hub 用户名
- `VERSION`: 版本号（如 `1.0.0`）或 `latest`

**脚本会询问是否构建多平台镜像 (amd64/arm64)，推荐选 Y**

---

## 第二部分：服务器端部署

### 方式 A：首次一键安装（推荐）

SSH 登录服务器后执行：

```bash
curl -fsSL https://raw.githubusercontent.com/unilei/kerkerker-douban-service/refs/heads/master/scripts/install.sh | bash
```

安装完成后会创建 `douban-service` 管理命令。

---

### 方式 B：Docker Compose 手动部署

#### 1. 克隆项目

```bash
git clone https://github.com/unilei/kerkerker-douban-service.git
cd kerkerker-douban-service
```

#### 2. 创建环境配置文件

```bash
cp .env.example .env
nano .env
```

**必须配置的环境变量：**

```env
# Docker Hub 用户名
DOCKER_USERNAME=your_username

# 管理面板密码（重要）
ADMIN_API_KEY=your_secure_password

# TMDB API Key（用于获取横向海报）
TMDB_API_KEY=your_tmdb_api_key

# 应用端口
PORT=8080
```

#### 3. 启动服务

```bash
docker-compose up -d
```

#### 4. 验证部署

```bash
# 检查健康状态
curl http://localhost:8080/health

# 检查服务状态
curl http://localhost:8080/api/v1/status
```

---

## 第三部分：更新已部署的服务

### 使用管理命令更新（一键安装后可用）

```bash
douban-service update
```

### 手动更新

```bash
# 1. 进入项目目录
cd /path/to/kerkerker-douban-service

# 2. 拉取最新镜像
docker pull YOUR_USERNAME/kerkerker-douban-service:latest

# 3. 重启服务
docker-compose down
docker-compose up -d

# 4. 清理旧镜像
docker image prune -f
```

---

## 第四部分：常用管理命令

| 命令                       | 功能     |
| -------------------------- | -------- |
| `douban-service start`     | 启动服务 |
| `douban-service stop`      | 停止服务 |
| `douban-service restart`   | 重启服务 |
| `douban-service logs`      | 查看日志 |
| `douban-service status`    | 查看状态 |
| `douban-service update`    | 更新镜像 |
| `douban-service config`    | 编辑配置 |
| `douban-service uninstall` | 卸载服务 |

---

## 第五部分：故障排查

### 查看容器日志

```bash
docker-compose logs -f douban-api
# 或
douban-service logs
```

### 检查容器状态

```bash
docker-compose ps
# 或
douban-service status
```

### 端口被占用

```bash
# 查看端口占用
lsof -i :8080

# 修改 .env 中的 PORT 配置后重启
nano .env
docker-compose restart
```

### Redis 连接失败

```bash
# 检查 Redis 容器状态
docker-compose ps redis

# 查看 Redis 日志
docker-compose logs redis
```

---

## 访问地址

- **API 服务**: `http://your-server:8080`
- **管理面板**: `http://your-server:8080/admin`
- **健康检查**: `http://your-server:8080/health`
