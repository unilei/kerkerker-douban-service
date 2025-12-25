# 🎬 Kerkerker Douban Service

<div align="center">

![Go Version](https://img.shields.io/badge/Go-1.23-00ADD8?style=flat-square&logo=go)
![License](https://img.shields.io/badge/License-MIT-green?style=flat-square)
![Docker](https://img.shields.io/badge/Docker-Ready-2496ED?style=flat-square&logo=docker)

**豆瓣数据 API 微服务** - 为 Kerkerker 项目提供电影、电视剧数据 API

[快速开始](#-快速开始) • [API 文档](#-api-端点) • [部署指南](#-服务器部署) • [管理面板](#-管理面板)

</div>

---

## ✨ 特性

- 🚀 **高性能** - Go + Gin 框架，响应速度快
- 💾 **多级缓存** - Redis 缓存层，减少 API 调用
- 🔀 **代理轮询** - 支持多代理负载均衡，突破 IP 限制
- 🎞️ **TMDB 集成** - 获取高质量横向海报
- 📊 **数据分析** - 内置 API 调用统计和性能监控
- 🛠️ **管理面板** - 可视化缓存管理和服务状态监控
- 🐳 **容器化** - 开箱即用的 Docker 部署方案

## 📦 技术栈

| 组件     | 技术                    |
| -------- | ----------------------- |
| 后端框架 | Go 1.23 + Gin           |
| 缓存     | Redis 7                 |
| 数据库   | MongoDB 7 (可选)        |
| 容器化   | Docker + Docker Compose |

## 🚀 快速开始

### 方式一：一键部署（推荐）

在服务器上执行以下命令：

```bash
curl -fsSL https://raw.githubusercontent.com/你的用户名/kerkerker-douban-service/main/scripts/install.sh | bash
```

### 方式二：Docker Compose

```bash
# 克隆项目
git clone https://github.com/你的用户名/kerkerker-douban-service.git
cd kerkerker-douban-service

# 创建配置文件
cp .env.example .env

# 编辑配置（可选：配置代理和 TMDB API）
nano .env

# 启动服务
docker-compose up -d

# 查看日志
docker-compose logs -f douban-api
```

### 方式三：本地开发

```bash
# 确保 Redis 和 MongoDB 运行中
# 安装依赖
go mod download

# 启动服务
go run cmd/server/main.go
```

## 📡 API 端点

### 数据接口

| 端点                 | 方法 | 说明             | 示例                                          |
| -------------------- | ---- | ---------------- | --------------------------------------------- |
| `/api/v1/hero`       | GET  | Hero Banner 数据 | `/api/v1/hero`                                |
| `/api/v1/latest`     | GET  | 最新内容         | `/api/v1/latest`                              |
| `/api/v1/movies`     | GET  | 电影分类         | `/api/v1/movies`                              |
| `/api/v1/tv`         | GET  | 电视剧分类       | `/api/v1/tv`                                  |
| `/api/v1/new`        | GET  | 新上线筛选       | `/api/v1/new`                                 |
| `/api/v1/category`   | GET  | 分类分页         | `/api/v1/category?category=hot_movies&page=1` |
| `/api/v1/detail/:id` | GET  | 影片详情         | `/api/v1/detail/1291546`                      |
| `/api/v1/search`     | GET  | 搜索影片         | `/api/v1/search?q=流浪地球`                   |

### 管理接口

| 端点                 | 方法   | 说明             |
| -------------------- | ------ | ---------------- |
| `/api/v1/status`     | GET    | 服务状态         |
| `/api/v1/analytics`  | GET    | API 统计数据     |
| `/api/v1/analytics`  | DELETE | 重置统计         |
| `/api/v1/{endpoint}` | DELETE | 清除指定端点缓存 |
| `/health`            | GET    | 健康检查         |

### 分类参数

`/api/v1/category` 端点支持以下分类：

| category 参数 | 说明       |
| ------------- | ---------- |
| `in_theaters` | 正在热映   |
| `hot_movies`  | 热门电影   |
| `hot_tv`      | 热门电视剧 |
| `us_tv`       | 美剧       |
| `jp_tv`       | 日剧       |
| `kr_tv`       | 韩剧       |
| `anime`       | 日本动画   |
| `documentary` | 纪录片     |
| `variety`     | 综艺       |
| `chinese_tv`  | 国产剧     |

## ⚙️ 环境变量

```env
# 服务配置
PORT=8080                          # 服务端口
GIN_MODE=release                   # 运行模式: debug/release

# Redis 配置
REDIS_URL=redis://localhost:6379   # Redis 连接地址

# MongoDB 配置 (可选)
MONGODB_URI=mongodb://localhost:27017
MONGODB_DATABASE=douban_api

# 豆瓣代理 (多个用逗号分隔)
DOUBAN_API_PROXY=https://proxy1.example.com,https://proxy2.example.com

# TMDB API (多个 Key 用逗号分隔，启用轮询)
TMDB_API_KEY=your_api_key_1,your_api_key_2
TMDB_BASE_URL=https://api.themoviedb.org/3
TMDB_IMAGE_BASE=https://image.tmdb.org/t/p/original
```

## 🖥️ 管理面板

访问 `http://your-server:8081` 即可打开管理面板：

### 功能模块

- **📊 数据分析** - API 调用统计、响应时间、缓存命中率
- **📡 API 端点** - 在线测试所有 API 接口
- **🗄️ 缓存管理** - 可视化管理各端点缓存

## 🌐 服务器部署

### 一键部署

```bash
# 使用 curl
curl -fsSL https://raw.githubusercontent.com/unilei/kerkerker-douban-service/refs/heads/master/scripts/install.sh | bash

# 使用 wget
wget -qO- https://raw.githubusercontent.com/你的用户名/kerkerker-douban-service/main/scripts/install.sh | bash
```

### 管理命令

部署完成后，使用以下命令管理服务：

```bash
douban-service start     # 启动服务
douban-service stop      # 停止服务
douban-service restart   # 重启服务
douban-service logs      # 查看日志
douban-service status    # 查看状态
douban-service update    # 更新到最新版本
douban-service config    # 编辑配置
douban-service uninstall # 卸载服务
```

## 📁 项目结构

```
.
├── cmd/server/              # 应用入口
│   └── main.go
├── internal/
│   ├── config/              # 配置管理
│   ├── handler/             # API 处理器
│   │   ├── admin.go         # 管理接口
│   │   ├── category.go      # 分类分页
│   │   ├── detail.go        # 影片详情
│   │   ├── hero.go          # Hero Banner
│   │   ├── latest.go        # 最新内容
│   │   ├── movies.go        # 电影分类
│   │   ├── new.go           # 新上线
│   │   ├── search.go        # 搜索
│   │   └── tv.go            # 电视剧分类
│   ├── middleware/          # 中间件
│   │   ├── cors.go          # 跨域处理
│   │   ├── logging.go       # 日志记录
│   │   └── metrics.go       # 性能统计
│   ├── model/               # 数据模型
│   ├── repository/          # 数据访问层
│   │   ├── cache.go         # Redis 缓存
│   │   └── metrics.go       # 统计存储
│   └── service/             # 业务逻辑层
│       ├── douban.go        # 豆瓣服务
│       └── tmdb.go          # TMDB 服务
├── pkg/httpclient/          # HTTP 客户端 (代理支持)
├── web/static/              # 管理面板前端
├── scripts/
│   ├── install.sh           # 一键部署脚本
│   └── docker-push.sh       # 镜像推送脚本
├── Dockerfile
├── docker-compose.yml
└── go.mod
```

## 🔗 在 Kerkerker 项目中使用

在 Kerkerker 项目的 `.env` 文件中添加：

```env
NEXT_PUBLIC_DOUBAN_API_URL=http://your-server:8081
```

然后在代码中调用：

```typescript
const response = await fetch(
  `${process.env.NEXT_PUBLIC_DOUBAN_API_URL}/api/v1/hero`
);
const data = await response.json();
```

## 🐳 Docker 镜像

### 拉取镜像

```bash
docker pull 你的用户名/kerkerker-douban-service:latest
```

### 推送镜像

```bash
# 使用推送脚本
./scripts/docker-push.sh -u 你的用户名 1.0.0
```

## 📄 License

MIT License © 2024

---

<div align="center">
Made with ❤️ for Kerkerker Project
</div>
