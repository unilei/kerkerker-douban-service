# 🎬 Kerkerker Douban Service

<div align="center">

![Go Version](https://img.shields.io/badge/Go-1.26.6-00ADD8?style=flat-square&logo=go)
![License](https://img.shields.io/badge/License-MIT-green?style=flat-square)
![Docker](https://img.shields.io/badge/Docker-Ready-2496ED?style=flat-square&logo=docker)

**豆瓣数据 API 微服务** - 为 Kerkerker 项目提供电影、电视剧数据 API

[快速开始](#-快速开始) • [持久化架构](#️-数据持久化架构) • [API 文档](#-api-端点) • [每日刷新](#每日刷新) • [部署指南](#-服务器部署) • [管理面板](#-管理面板)

</div>

---

## ✨ 特性

- 🚀 **高性能** - Go + Gin 框架，响应速度快
- 💾 **三层存储** - Redis 热缓存 → MongoDB 持久真相源 → 豆瓣冷源，缓存过期后不再重复抓取
- 🆔 **内部 ID 关联键** - 每部影片分配稳定的自增 `internal_id`，作为跨数据源（VOD/网盘）的关联主键
- 🔄 **每日定时刷新** - 每天低峰期自动刷新陈旧影片数据并增量同步图片
- 🔀 **代理轮询** - 支持多代理负载均衡，突破 IP 限制
- ☁️ **R2 图片镜像** - 自动同步豆瓣图片到 Cloudflare R2，映射持久化到 Mongo，重启后不重复上传
- 🎞️ **TMDB 集成** - 获取高质量横向海报
- 📊 **数据分析** - 内置 API 调用统计和性能监控
- 🔐 **安全认证** - Admin API Key 保护管理接口
- 🛠️ **管理面板** - 可视化缓存管理和服务状态监控
- 🐳 **容器化** - 开箱即用的 Docker 部署方案

## 📦 技术栈

| 组件     | 技术                            |
| -------- | ------------------------------- |
| 后端框架 | Go 1.26.6 + Gin                 |
| 热缓存   | Redis 7                         |
| 持久层   | MongoDB 7（可选，未配置自动降级） |
| 容器化   | Docker + Docker Compose         |

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

# 手动跑一次每日刷新（详见「每日刷新」章节）
go run cmd/refresh/main.go --max-age=24h --limit=500
```

## 🗄️ 数据持久化架构

配置 `MONGO_URI` 后，服务从「纯 Redis 缓存」升级为三层存储，豆瓣只在 Mongo 未命中时被回源：

```
请求 ──► ① Redis 热缓存（秒级，TTL 到期自动过期）
          │ 未命中
          ▼
        ② MongoDB 持久真相源（永不过期，重启/Redis 清空后兜底）
          │ 未命中
          ▼
        ③ 豆瓣冷源（回源抓取 → 落库 Mongo → 回填 Redis）
```

**降级行为**：`MONGO_URI` 未配置或 Mongo 不可用时，自动退回旧的 Redis-only 模式（仅记日志，不阻断服务），完全向后兼容。

### MongoDB 集合

| 集合            | 用途                                                                 |
| --------------- | -------------------------------------------------------------------- |
| `movies`        | 影片详情快照。`douban_id` 唯一索引，含完整 `detail`、刷新状态字段     |
| `counters`      | `internal_id` 原子自增分配器（`findAndModify $inc`）                  |
| `image_mappings`| 原图 URL → R2 镜像 URL 的持久映射，进程重启后复用已上传对象           |
| `snapshots`     | 列表型端点（hero/movies/tv/new/search/Top 250 等）的载荷快照，按缓存键存储 |

### internal_id 关联键

每部影片首次落库时分配一个**自增整数 `internal_id`**（与豆瓣 ID 解耦、永不变化），作为跨数据源关联的主键 —— 例如网盘链接、VOD 匹配等下游功能统一用它关联，不受豆瓣 ID 变更影响。

- 详情响应自动带 `internal_id` 字段
- 规范化查询端点：`GET /api/v1/movies/:internal_id`
- 兼容入口 `GET /api/v1/detail/:douban_id` 保留（legacy alias），同样返回 `internal_id`

### 每日刷新

`cmd/refresh` 二进制扫描 `refresh_status=stale` 或超过 24h 未刷新的影片，重新回源豆瓣更新 Mongo 并增量同步图片。**主通道是部署服务器上的 crontab**（douban-mongo 通常仅 docker 内网可达，GitHub runner 无法直连）：

```bash
# crontab -e（每天北京时间 02:00 低峰期执行）
0 2 * * * flock -n /tmp/douban-refresh.lock docker run --rm \
  --network kerkerker-douban-service_douban-network \
  --env-file /opt/kerkerker-douban-service/douban.env \
  ghcr.io/unilei/kerkerker-douban-service:latest \
  /app/refresh --max-age=24h --limit=500 >> /var/log/douban-refresh.log 2>&1
```

常用参数：`--max-age`（陈旧阈值，默认 24h）、`--limit`（单次上限，默认 500）、`--dry-run`（只列出待刷新条目）。

刷新任务支持可选的 `kerkerker.plugin-job.v1` 机器可读进度协议，默认关闭：

| `KERKERKER_JOB_REPORT` | 行为 |
| --- | --- |
| 空 | 不生成任务事件，保持原有行为 |
| `stdout` | 写入以 `KERKERKER_JOB_EVENT ` 开头的 JSON 行 |
| `http` | 通过 Bearer 认证 POST 到宿主任务接收器 |
| `both` | 同一事件同时写 stdout 和 HTTP，序号、ID、时间戳完全一致 |

HTTP 模式需要在服务器的 `douban.env` 中配置：

```env
KERKERKER_JOB_REPORT=http
KERKERKER_JOB_REPORT_URL=https://your-host.example/api/plugins/jobs/report
KERKERKER_JOB_REPORT_TOKEN=replace-with-the-same-dedicated-host-secret
KERKERKER_PLUGIN_ID=kerkerker.douban-content
KERKERKER_PLUGIN_VERSION=1.0.0
KERKERKER_PLUGIN_PROFILE=cn-default
KERKERKER_PLUGIN_CONFIG_VERSION=runtime
KERKERKER_JOB_ACTOR=system/refresh
```

每次运行先发送 `sequence=0` 的 `started`，再发送累计进度和终态；`event_id` 固定为 `<run_id>:<sequence>`。HTTP 临时错误、`409` 和限流最多重试三次，重定向不会跟随，响应体不会写入日志。远程 URL 必须使用 HTTPS；只有 `localhost`、`127.0.0.1` 和 `::1` 可以使用 HTTP。上报最终失败只记录脱敏警告，不阻断影片刷新；当前没有跨进程持久 spool，因此宿主允许序号跳跃，但不会允许状态或累计进度倒退。

Web 宿主必须配置同一个独立 `KERKERKER_JOB_REPORT_TOKEN`。密钥必须是 32–512 位 URL-safe 字符，建议使用 `openssl rand -hex 32` 生成；不要复用管理员密码、会话密钥、Admin API Key 或其它 cron 密钥。该接入目前提供持久进度可见性，不提供宿主远程取消或刷新断点恢复。

`cn-compliance` 自动部署支持用仓库变量 `DEPLOY_JOB_REPORT_MODE=http|both` 显式开启，并从 Secrets 读取 `DEPLOY_JOB_REPORT_URL`、`DEPLOY_JOB_REPORT_TOKEN`。流水线先向 HTTPS 接收器发送无效空事件并要求返回 `400`，同时验证地址和密钥，再以事务方式更新服务器 `douban.env`；失败时恢复旧文件。清空该变量会在下次部署时清除 URL 和密钥并关闭上报。Compose 覆盖层会把三个上报变量在长期运行的 `douban-api` 容器中强制清空，只有读取 `douban.env` 的一次性 refresh 容器能访问密钥。Web 与 Go 两个仓库中的 `DEPLOY_JOB_REPORT_TOKEN` 必须设置为同一随机值。

`.github/workflows/refresh.yml` 仅保留 `workflow_dispatch` 手动触发（需在 Secrets 配置公网可达的 `MONGO_URI`），用于临时补数据；定时任务以服务器 cron 为准。

## 📡 API 端点

### 数据接口

| 端点                      | 方法 | 说明             | 示例                                          |
| ------------------------- | ---- | ---------------- | --------------------------------------------- |
| `/api/v1/hero`            | GET  | Hero Banner 数据 | `/api/v1/hero`                                |
| `/api/v1/latest`          | GET  | 最新内容         | `/api/v1/latest`                              |
| `/api/v1/movies`          | GET  | 电影分类         | `/api/v1/movies`                              |
| `/api/v1/tv`              | GET  | 电视剧分类       | `/api/v1/tv`                                  |
| `/api/v1/new`             | GET  | 新上线筛选       | `/api/v1/new`                                 |
| `/api/v1/category`        | GET  | 分类分页         | `/api/v1/category?category=hot_movies&page=1` |
| `/api/v1/250`             | GET  | 完整豆瓣 Top 250 榜单 | `/api/v1/250`                            |
| `/api/v1/detail/:id`      | GET  | 影片详情（豆瓣 ID，legacy 兼容） | `/api/v1/detail/1291546`            |
| `/api/v1/movies/:internal_id` | GET | 影片详情（内部 ID，规范化入口） | `/api/v1/movies/42`                |
| `/api/v1/search`          | GET  | 搜索影片         | `/api/v1/search?q=流浪地球`                   |
| `/api/v1/calendar`        | GET  | 追剧日历         | `/api/v1/calendar?start_date=2026-01-09`      |
| `/api/v1/calendar/airing` | GET  | 今日热播         | `/api/v1/calendar/airing?region=CN`           |

`/api/v1/new` 的筛选分页使用 `page`（1–10000）与 `pageSize`（1–100）。显式传入 `sort=recommend|time|rank` 会进入筛选列表语义；Redis 和 Mongo 快照均保存 `data + pagination`，缓存键包含筛选条件、页码与页大小，不同页不会互相覆盖。

### 获取豆瓣 Top 250

`GET /api/v1/250` 一次返回按官方排名排序的完整 250 条影片。冷源使用豆瓣移动端 Top 250 集合，不会用“热门电影”或“豆瓣高分”等分类结果替代。

```bash
curl http://localhost:8080/api/v1/250
```

```json
{
  "code": 200,
  "data": {
    "subjects": [
      {
        "id": "1292052",
        "title": "肖申克的救赎",
        "rate": "9.7",
        "cover": "https://img3.doubanio.com/view/photo/m_ratio_poster/public/p2934829882.jpg",
        "url": "https://movie.douban.com/subject/1292052/"
      }
    ],
    "fetched_at": "2026-08-20T06:03:22Z"
  },
  "source": "fresh-data"
}
```

`source` 表示本次数据来源：

| 值 | 说明 |
| --- | --- |
| `redis-cache` | Redis 热缓存命中 |
| `mongo-snapshot` | Mongo 快照仍在新鲜期，并已回填 Redis |
| `fresh-data` | 从豆瓣冷源获取并写入 Redis/Mongo |
| `redis-stale` / `mongo-stale` | 快照已过期且豆瓣暂时不可用，返回最后一份完整旧榜单 |

只有完整、排名连续且豆瓣 ID 唯一的 250 条数据才会写入缓存。没有可用旧快照且豆瓣冷源失败时返回 `502`：

```json
{
  "code": 502,
  "error": "获取豆瓣 Top 250 失败"
}
```

启用 R2 图片同步时，Top 250 封面会在响应后于后台镜像，并更新 Redis 与 Mongo 快照，避免首次请求等待 250 张图片上传。

### 管理接口

| 端点                 | 方法   | 说明             |
| -------------------- | ------ | ---------------- |
| `/api/v1/status`     | GET    | 服务状态         |
| `/api/v1/analytics`  | GET    | API 统计数据     |
| `/api/v1/analytics`  | DELETE | 重置统计         |
| `/api/v1/{endpoint}` | DELETE | 清除指定端点缓存 |
| `/health`            | GET    | 健康检查         |

`DELETE /api/v1/250` 需要 Admin API Key（启用认证时），会同时删除 Redis 热缓存和 Mongo 持久快照，使下一次 `GET /api/v1/250` 真正从豆瓣重新获取。

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

# MongoDB 持久层 (可选；未配置则降级为 Redis-only 旧模式)
MONGO_URI=mongodb://localhost:27017/kerkerker_douban
MONGO_DB_NAME=kerkerker_douban

# 豆瓣代理 (多个用逗号分隔)
DOUBAN_API_PROXY=https://proxy1.example.com,https://proxy2.example.com

# TMDB API (多个 Key 用逗号分隔，启用轮询)
TMDB_API_KEY=your_api_key_1,your_api_key_2
TMDB_BASE_URL=https://api.themoviedb.org/3
TMDB_IMAGE_BASE=https://image.tmdb.org/t/p/original

# Cloudflare R2 图片同步
# 推荐：通过鉴权 Upload Worker 上传
CLOUDFLARE_R2_PUBLIC_URL=https://douban-images.example.com
CLOUDFLARE_R2_UPLOAD_API_URL=https://example-upload.workers.dev/objects
CLOUDFLARE_R2_UPLOAD_API_TOKEN=your_upload_token
CLOUDFLARE_R2_KEY_PREFIX=douban-images
CLOUDFLARE_R2_MAX_IMAGE_BYTES=10485760
# 生产环境建议启用；配置不完整时服务直接拒绝启动
REQUIRE_R2_IMAGE_SYNC=true

# 可选替代方案：直接使用 R2 S3 API 上传
# 可使用 ACCOUNT_ID 自动生成 Endpoint，也可以直接配置 CLOUDFLARE_R2_ENDPOINT
CLOUDFLARE_R2_ACCOUNT_ID=your_account_id
CLOUDFLARE_R2_ENDPOINT=
CLOUDFLARE_R2_ACCESS_KEY_ID=your_access_key_id
CLOUDFLARE_R2_SECRET_ACCESS_KEY=your_secret_access_key
CLOUDFLARE_R2_BUCKET=douban-images

# Admin API 认证 (重要!)
ADMIN_API_KEY=your_secure_key      # 设置后管理接口需要认证

# 缓存 TTL 配置 (单位：分钟)
CACHE_TTL_HERO=360                 # Hero Banner 缓存，默认 6 小时
CACHE_TTL_DETAIL=1440              # 详情页缓存，默认 24 小时
CACHE_TTL_CATEGORY=60              # 分类及 Top 250 新鲜期，默认 1 小时
CACHE_TTL_SEARCH=30                # 搜索缓存，默认 30 分钟
CACHE_TTL_DEFAULT=60               # 默认缓存，默认 1 小时
```

R2 配置完整后，服务会在公开接口返回前将豆瓣域名图片上传到 R2，并把缓存和响应中的图片地址改为 `CLOUDFLARE_R2_PUBLIC_URL`。上传失败时保留原豆瓣图片地址，不会阻断数据接口。`CLOUDFLARE_R2_PUBLIC_URL` 必须指向 Bucket 根目录的公开域名，生产环境推荐绑定 Cloudflare R2 自定义域名，不使用 `r2.dev` 开发地址。

配置 `MONGO_URI` 后，原图 → R2 镜像的映射会持久化到 `image_mappings` 集合：进程重启后直接复用已上传对象，不再重复上传；同一进程内并发请求同一图片也会被 singleflight 去重，只有一个请求真正执行下载与上传。

推荐部署 `cloudflare/image-upload-worker`，将它绑定到目标 Bucket，并通过 `wrangler secret put UPLOAD_TOKEN` 设置上传密钥。服务只需要公开 R2 URL、Worker `/objects` 地址和密钥；若不使用 Worker，也可以配置完整的 R2 S3 API 凭证直接上传。

## 🖥️ 管理面板

访问 `http://your-server:8081/admin` 即可打开管理面板。

### 登录认证

如果配置了 `ADMIN_API_KEY` 环境变量，访问管理面板时需要登录：

1. 打开管理面板会显示登录页面
2. 输入设置的 `ADMIN_API_KEY` 值
3. 登录成功后进入仪表盘

> ℹ️ 如果未配置 `ADMIN_API_KEY`，管理接口将对外开放（不推荐用于生产环境）

### 功能模块

- **📊 数据分析** - API 调用统计、响应时间、缓存命中率
- **📡 API 端点** - 在线测试所有 API 接口
- **🗄️ 缓存管理** - 可视化管理各端点缓存

### 管理 API 认证

调用管理 API 时需在请求头中带上认证：

```bash
curl -H "Authorization: Bearer YOUR_ADMIN_API_KEY" http://localhost:8081/api/v1/analytics
```

## 🌐 服务器部署

### `cn-compliance` 分支自动部署

`.github/workflows/deploy-cn-compliance.yml` 为当前合规分支提供独立流水线。推送到
`cn-compliance` 后会依次执行格式检查、测试、`go vet` 和构建，发布带提交 SHA 的
`linux/amd64` GHCR 镜像，再通过 SSH 只重建服务器现有 Compose 项目中的
`douban-api` 服务。工作流不会上传或覆盖服务器的运行时 env-file，也不会替换 Redis、
Mongo、网络和数据卷；它会通过临时 Compose override 把该 env-file 中的 Mongo 与 R2
配置传入容器，部署结束后立即删除 override。

部署成功必须同时满足 `/health` 返回成功、`/api/v1/status` 报告 R2 同步和 Mongo 映射
持久化均已启用，以及 `/api/v1/250` 的 250 张封面全部使用当前 R2 公网前缀。任一检查
失败都会把 `latest` 标签和 `douban-api` 容器恢复到部署前镜像。

仓库需要配置以下 Actions Secrets：

| Secret | 作用 |
| --- | --- |
| `DEPLOY_HOST` / `DEPLOY_USER` | VPS 地址和 SSH 用户 |
| `DEPLOY_SSH_KEY` | 仅具备部署所需权限的 SSH 私钥 |
| `DEPLOY_KNOWN_HOSTS` | 预先核验并固定的 SSH 主机指纹 |

仓库 Variables 中的 `DEPLOY_PATH` 必须指向服务器现有 Compose 目录，其中的服务名必须为
`douban-api`；`DEPLOY_ENV_FILE` 指向服务器现有运行时配置，默认
`/opt/kerkerker-douban-service/douban.env`，必须同时包含 `MONGO_URI` 和一组完整 R2 上传
配置；`DEPLOY_PORT` 是宿主机健康检查端口，默认 `8081`。私有部署仓库可通过
`IMAGE_NAME` 指定其私有 GHCR 制品名；`DEPLOY_STABLE_IMAGE_NAME` 必须与服务器现有 Compose
引用的镜像名一致，默认 `ghcr.io/unilei/kerkerker-douban-service`。

GitHub 不支持在公开仓库中单独隐藏某个分支。若 `cn-compliance` 的实现不能公开，必须在
私有仓库运行这套流水线，或先将整个仓库改为私有；不得把待保密提交推到公开 origin。

### 第一步：发布镜像

推送到 `master` 后，GitHub Actions 会自动构建 amd64/arm64 镜像并发布到 GHCR：

```bash
docker pull ghcr.io/unilei/kerkerker-douban-service:latest
```

也可以使用 GitHub Personal Access Token 手动发布：

```bash
export GITHUB_TOKEN=YOUR_GITHUB_TOKEN
./scripts/docker-push.sh 1.0.0
```

---

### 第二步：服务器端部署

#### 方式 A：一键安装（推荐）

```bash
# 使用 curl
curl -fsSL https://raw.githubusercontent.com/unilei/kerkerker-douban-service/refs/heads/master/scripts/install.sh | bash
```

#### 方式 B：Docker Compose 手动部署

```bash
# 1. 克隆项目
git clone https://github.com/unilei/kerkerker-douban-service.git
cd kerkerker-douban-service

# 2. 创建配置文件
cp .env.example .env
nano .env  # 编辑必要的环境变量

# 3. 启动服务
docker-compose up -d

# 4. 验证部署
curl http://localhost:8080/health
```

---

### 启用 MongoDB 持久层（推荐）

Docker Compose 默认只含 `douban-api` + `redis`。要启用三层存储与 `internal_id`，需要加一个 Mongo 服务并配置连接：

```yaml
# docker-compose.yml 追加
  douban-mongo:
    image: mongo:7
    restart: unless-stopped
    volumes:
      - douban-mongo-data:/data/db
    networks:
      - douban-network

volumes:
  douban-mongo-data:
```

```env
# .env 追加（compose 网络内的服务别名是 douban-mongo）
MONGO_URI=mongodb://douban-mongo:27017/kerkerker_douban
MONGO_DB_NAME=kerkerker_douban
```

已有部署按 docker run 方式管理的，把环境变量写入 env-file 后重建容器即可（生产示例）：

```bash
# 导出现有容器环境变量并追加 Mongo 配置
docker inspect kerkerker-douban-service --format '{{range .Config.Env}}{{println .}}{{end}}' \
  | grep -v '^PATH=' > /opt/kerkerker-douban-service/douban.env
echo 'MONGO_URI=mongodb://mongo:27017/kerkerker_douban' >> /opt/kerkerker-douban-service/douban.env

# 重建（网络、端口、healthcheck 参数保持与原容器一致）
docker pull ghcr.io/unilei/kerkerker-douban-service:latest
docker stop kerkerker-douban-service && docker rm kerkerker-douban-service
docker run -d --name kerkerker-douban-service \
  --network kerkerker-douban-service_douban-network \
  -p 8081:8080 --restart unless-stopped \
  --health-cmd "wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1" \
  --health-interval 30s --health-timeout 3s --health-start-period 5s --health-retries 3 \
  --env-file /opt/kerkerker-douban-service/douban.env \
  ghcr.io/unilei/kerkerker-douban-service:latest
```

启动日志出现 `✅ MongoDB connected` 即接入成功；未配置 `MONGO_URI` 时日志会提示 Redis-only 模式，服务行为与旧版一致。

---

### 第三步：更新已部署的服务

#### 使用管理命令（一键安装后可用）

```bash
douban-service update
```

#### 手动更新

```bash
# 拉取最新镜像
docker pull ghcr.io/unilei/kerkerker-douban-service:latest

# 重启服务
docker-compose down
docker-compose up -d

# 清理旧镜像
docker image prune -f
```

---

### 管理命令

部署完成后，使用以下命令管理服务：

| 命令                       | 功能           |
| -------------------------- | -------------- |
| `douban-service start`     | 启动服务       |
| `douban-service stop`      | 停止服务       |
| `douban-service restart`   | 重启服务       |
| `douban-service logs`      | 查看日志       |
| `douban-service status`    | 查看状态       |
| `douban-service update`    | 更新到最新版本 |
| `douban-service config`    | 编辑配置       |
| `douban-service uninstall` | 卸载服务       |

---

### 故障排查

```bash
# 查看容器日志
docker-compose logs -f douban-api

# 检查容器状态
docker-compose ps

# 检查端口占用
lsof -i :8080

# 查看 Redis 状态
docker-compose logs redis
```

## 📁 项目结构

```
.
├── cmd/
│   ├── server/              # API 服务入口
│   │   └── main.go
│   └── refresh/             # 每日刷新二进制（cron 执行，更新 Mongo + 同步图片）
│       └── main.go
├── internal/
│   ├── config/              # 配置管理
│   ├── handler/             # API 处理器
│   │   ├── admin.go         # 管理接口
│   │   ├── category.go      # 分类分页
│   │   ├── detail.go        # 影片详情（三层存储 + /movies/:internal_id）
│   │   ├── hero.go          # Hero Banner
│   │   ├── latest.go        # 最新内容
│   │   ├── movies.go        # 电影分类
│   │   ├── new.go           # 新上线（筛选分支持久化分页信息）
│   │   ├── search.go        # 搜索
│   │   └── tv.go            # 电视剧分类
│   ├── middleware/          # 中间件
│   │   ├── cors.go          # 跨域处理
│   │   ├── logging.go       # 日志记录
│   │   └── metrics.go       # 性能统计
│   ├── model/               # 数据模型
│   ├── repository/          # 数据访问层
│   │   ├── cache.go         # Redis 缓存
│   │   ├── metrics.go       # 统计存储
│   │   ├── movie_store.go   # movies 集合 + internal_id 自增分配
│   │   ├── image_map_store.go   # image_mappings 集合（R2 映射持久化）
│   │   ├── snapshot_store.go    # snapshots 集合（列表端点兜底）
│   │   └── mongo_stores.go  # Mongo 客户端共享装配
│   └── service/             # 业务逻辑层
│       ├── douban.go        # 豆瓣服务（含 FetchDetail 共享抓取）
│       ├── image_syncer.go  # R2 图片同步（singleflight 去重）
│       └── tmdb.go          # TMDB 服务
├── pkg/httpclient/          # HTTP 客户端 (代理支持)
├── web/static/              # 管理面板前端
├── .github/workflows/
│   ├── publish-image.yml    # master 推送自动构建 GHCR 镜像
│   └── refresh.yml          # 手动触发补数据（定时主通道是服务器 cron）
├── scripts/
│   ├── install.sh           # 一键部署脚本
│   └── docker-push.sh       # 镜像推送脚本
├── Dockerfile               # 同时构建 server + refresh 双二进制
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
  `${process.env.NEXT_PUBLIC_DOUBAN_API_URL}/api/v1/hero`,
);
const data = await response.json();
```

详情响应会带 `internal_id` 字段；前端类型见 `kerkerker/lib/douban-service.ts` 的 `SubjectDetail`。下游功能（VOD 匹配、网盘链接等）应以 `internal_id` 作为关联键，通过 `GET /api/v1/movies/:internal_id` 反查，不依赖豆瓣 ID。

## 🐳 Docker 镜像

### 拉取镜像

```bash
docker pull ghcr.io/unilei/kerkerker-douban-service:latest
```

### 推送镜像

```bash
# 推荐：推送到 master，由 GitHub Actions 自动发布
git push origin master

# 手动发布（需要 GITHUB_TOKEN）
GITHUB_TOKEN=YOUR_GITHUB_TOKEN ./scripts/docker-push.sh 1.0.0
```

## 📄 License

MIT License © 2024

---

<div align="center">
Made with ❤️ for Kerkerker Project
</div>
