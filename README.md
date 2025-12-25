# Kerkerker Douban Service

豆瓣数据 API 微服务，为 kerkerker 项目提供豆瓣相关的 API 服务。

## 技术栈

- **Go 1.23** + Gin Web Framework
- **MongoDB** - 数据持久化（可选）
- **Redis** - 缓存层

## 快速开始

### 使用 Docker Compose（推荐）

```bash
# 创建环境变量文件
cp .env.example .env

# 编辑 .env 文件，配置必要的环境变量
# 尤其是 DOUBAN_API_PROXY 和 TMDB_API_KEY

# 启动服务
docker-compose up -d

# 查看日志
docker-compose logs -f douban-api
```

### 本地开发

```bash
# 确保 Redis 运行中
# 安装依赖
go mod download

# 启动服务
go run cmd/server/main.go
```

## API 端点

| 端点                 | 方法 | 说明             |
| -------------------- | ---- | ---------------- |
| `/api/v1/hero`       | GET  | Hero Banner 数据 |
| `/api/v1/category`   | GET  | 分类分页数据     |
| `/api/v1/detail/:id` | GET  | 影片详情         |
| `/api/v1/latest`     | GET  | 最新内容         |
| `/api/v1/movies`     | GET  | 电影分类         |
| `/api/v1/tv`         | GET  | 电视剧分类       |
| `/api/v1/new`        | GET  | 新上线筛选       |
| `/api/v1/search`     | GET  | 搜索             |
| `/health`            | GET  | 健康检查         |

## 环境变量

```env
PORT=8080
GIN_MODE=debug
REDIS_URL=redis://localhost:6379
MONGODB_URI=mongodb://localhost:27017
MONGODB_DATABASE=douban_api
DOUBAN_API_PROXY=https://proxy1.workers.dev,https://proxy2.workers.dev
TMDB_API_KEY=your_tmdb_api_key
TMDB_BASE_URL=https://api.themoviedb.org/3
TMDB_IMAGE_BASE=https://image.tmdb.org/t/p/original
```

## 项目结构

```
.
├── cmd/server/          # 入口文件
├── internal/
│   ├── config/          # 配置管理
│   ├── handler/         # API 处理器
│   ├── middleware/      # 中间件
│   ├── model/           # 数据模型
│   ├── repository/      # 数据访问层
│   └── service/         # 业务逻辑层
├── pkg/httpclient/      # HTTP 客户端
├── Dockerfile
├── docker-compose.yml
└── go.mod
```

## 在 kerkerker 项目中使用

在 kerkerker 项目的 `.env` 文件中添加：

```env
DOUBAN_API_URL=http://localhost:8080
```

将 API 调用改为：

```typescript
const response = await fetch(`${process.env.DOUBAN_API_URL}/api/v1/hero`);
```
