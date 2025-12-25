#!/bin/bash

# =============================================================================
# Kerkerker Douban Service 一键部署脚本
# 
# 用法: curl -fsSL https://raw.githubusercontent.com/你的用户名/kerkerker-douban-service/main/scripts/install.sh | bash
# 或者: wget -qO- https://raw.githubusercontent.com/你的用户名/kerkerker-douban-service/main/scripts/install.sh | bash
# =============================================================================

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
MAGENTA='\033[0;35m'
NC='\033[0m' # No Color
BOLD='\033[1m'

# 配置
DOCKER_IMAGE="${DOCKER_IMAGE:-leizhe/kerkerker-douban-service}"
INSTALL_DIR="${INSTALL_DIR:-/opt/kerkerker-douban-service}"
SERVICE_PORT="${SERVICE_PORT:-8081}"

# 打印带颜色的消息
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[✓]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[✗]${NC} $1"
}

log_step() {
    echo -e "\n${CYAN}${BOLD}▶ $1${NC}"
}

# 显示 Banner
show_banner() {
    echo -e "${MAGENTA}"
    cat << 'EOF'
╔═══════════════════════════════════════════════════════════════╗
║                                                               ║
║   🎬 Kerkerker Douban Service 一键部署                        ║
║                                                               ║
║   豆瓣 API 代理服务 - 支持电影、电视剧数据获取                   ║
║                                                               ║
╚═══════════════════════════════════════════════════════════════╝
EOF
    echo -e "${NC}"
}

# 检查是否为 root 用户
check_root() {
    if [ "$EUID" -ne 0 ]; then
        log_warn "建议使用 root 用户运行此脚本"
        log_info "尝试使用 sudo 继续..."
        SUDO="sudo"
    else
        SUDO=""
    fi
}

# 检查系统
check_system() {
    log_step "检查系统环境"
    
    # 检查操作系统
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        OS=$NAME
        log_info "操作系统: $OS"
    else
        log_warn "无法识别操作系统"
    fi
    
    # 检查架构
    ARCH=$(uname -m)
    log_info "系统架构: $ARCH"
    
    log_success "系统检查完成"
}

# 检查并安装 Docker
check_docker() {
    log_step "检查 Docker"
    
    if command -v docker &> /dev/null; then
        DOCKER_VERSION=$(docker --version | cut -d ' ' -f3 | tr -d ',')
        log_success "Docker 已安装 (版本: $DOCKER_VERSION)"
    else
        log_warn "Docker 未安装，正在安装..."
        install_docker
    fi
    
    # 检查 Docker 是否运行
    if ! docker info &> /dev/null; then
        log_warn "Docker 未运行，正在启动..."
        $SUDO systemctl start docker
        $SUDO systemctl enable docker
    fi
    
    log_success "Docker 运行正常"
}

# 安装 Docker
install_docker() {
    log_info "正在安装 Docker..."
    
    # 使用官方脚本安装
    curl -fsSL https://get.docker.com | $SUDO sh
    
    # 将当前用户添加到 docker 组
    if [ -n "$SUDO_USER" ]; then
        $SUDO usermod -aG docker $SUDO_USER
    elif [ -n "$USER" ] && [ "$USER" != "root" ]; then
        $SUDO usermod -aG docker $USER
    fi
    
    # 启动 Docker
    $SUDO systemctl start docker
    $SUDO systemctl enable docker
    
    log_success "Docker 安装完成"
}

# 检查并安装 Docker Compose
check_docker_compose() {
    log_step "检查 Docker Compose"
    
    if docker compose version &> /dev/null; then
        COMPOSE_VERSION=$(docker compose version --short)
        log_success "Docker Compose 已安装 (版本: $COMPOSE_VERSION)"
    elif command -v docker-compose &> /dev/null; then
        COMPOSE_VERSION=$(docker-compose --version | cut -d ' ' -f4 | tr -d ',')
        log_success "Docker Compose 已安装 (版本: $COMPOSE_VERSION)"
        DOCKER_COMPOSE="docker-compose"
    else
        log_warn "Docker Compose 未安装，正在安装..."
        install_docker_compose
    fi
    
    # 默认使用新版命令
    DOCKER_COMPOSE="${DOCKER_COMPOSE:-docker compose}"
}

# 安装 Docker Compose
install_docker_compose() {
    log_info "正在安装 Docker Compose..."
    
    # Docker Compose V2 通常随 Docker 一起安装
    # 如果没有，尝试安装插件
    $SUDO mkdir -p /usr/local/lib/docker/cli-plugins
    $SUDO curl -SL https://github.com/docker/compose/releases/latest/download/docker-compose-linux-$(uname -m) -o /usr/local/lib/docker/cli-plugins/docker-compose
    $SUDO chmod +x /usr/local/lib/docker/cli-plugins/docker-compose
    
    log_success "Docker Compose 安装完成"
}

# 创建安装目录
create_install_dir() {
    log_step "创建安装目录"
    
    if [ -d "$INSTALL_DIR" ]; then
        log_warn "安装目录已存在: $INSTALL_DIR"
        read -p "是否覆盖? [y/N] " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            log_info "使用现有目录"
        fi
    else
        $SUDO mkdir -p "$INSTALL_DIR"
        log_success "创建目录: $INSTALL_DIR"
    fi
    
    cd "$INSTALL_DIR"
}

# 配置环境变量
configure_env() {
    log_step "配置环境变量"
    
    # 检查是否已有配置
    if [ -f "$INSTALL_DIR/.env" ]; then
        log_warn "已存在配置文件"
        read -p "是否重新配置? [y/N] " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            log_info "使用现有配置"
            return
        fi
    fi
    
    echo ""
    echo -e "${CYAN}${BOLD}请配置以下选项 (直接回车使用默认值):${NC}"
    echo ""
    
    # 服务端口
    read -p "服务端口 [${SERVICE_PORT}]: " input_port
    SERVICE_PORT="${input_port:-$SERVICE_PORT}"
    
    # 豆瓣代理
    echo ""
    log_info "豆瓣代理用于绕过 IP 限制，多个代理用逗号分隔"
    log_info "格式: http://ip:port 或 http://user:pass@ip:port"
    read -p "豆瓣代理 (可选): " DOUBAN_PROXY
    
    # TMDB API
    echo ""
    log_info "TMDB API 用于获取横向海报，提升 Hero Banner 效果"
    log_info "获取地址: https://www.themoviedb.org/settings/api"
    log_info "多个 API Key 用逗号分隔，将启用轮询负载均衡"
    read -p "TMDB API Key (可选): " TMDB_KEY
    
    # 写入配置文件
    cat > "$INSTALL_DIR/.env" << EOF
# Kerkerker Douban Service 配置文件
# 生成时间: $(date '+%Y-%m-%d %H:%M:%S')

# 服务端口
SERVICE_PORT=${SERVICE_PORT}

# 豆瓣代理 (多个用逗号分隔)
DOUBAN_API_PROXY=${DOUBAN_PROXY}

# TMDB API 配置 (多个 Key 用逗号分隔)
TMDB_API_KEY=${TMDB_KEY}
TMDB_BASE_URL=https://api.themoviedb.org/3
TMDB_IMAGE_BASE=https://image.tmdb.org/t/p/original
EOF

    log_success "配置文件已保存"
}

# 创建 docker-compose.yml
create_docker_compose() {
    log_step "创建 Docker Compose 配置"
    
    cat > "$INSTALL_DIR/docker-compose.yml" << EOF
services:
  douban-api:
    image: ${DOCKER_IMAGE}:latest
    container_name: kerkerker-douban-service
    ports:
      - "\${SERVICE_PORT:-8081}:8080"
    environment:
      - PORT=8080
      - GIN_MODE=release
      - MONGODB_URI=mongodb://mongo:27017
      - MONGODB_DATABASE=douban_api
      - REDIS_URL=redis://redis:6379
      - DOUBAN_API_PROXY=\${DOUBAN_API_PROXY:-}
      - TMDB_API_KEY=\${TMDB_API_KEY:-}
      - TMDB_BASE_URL=\${TMDB_BASE_URL:-https://api.themoviedb.org/3}
      - TMDB_IMAGE_BASE=\${TMDB_IMAGE_BASE:-https://image.tmdb.org/t/p/original}
    depends_on:
      - mongo
      - redis
    restart: unless-stopped
    networks:
      - douban-network

  mongo:
    image: mongo:7
    container_name: kerkerker-mongo
    volumes:
      - mongo_data:/data/db
    restart: unless-stopped
    networks:
      - douban-network

  redis:
    image: redis:7-alpine
    container_name: kerkerker-redis
    volumes:
      - redis_data:/data
    command: redis-server --appendonly yes
    restart: unless-stopped
    networks:
      - douban-network

networks:
  douban-network:
    driver: bridge

volumes:
  mongo_data:
  redis_data:
EOF

    log_success "Docker Compose 配置已创建"
}

# 拉取镜像
pull_images() {
    log_step "拉取 Docker 镜像"
    
    log_info "拉取 ${DOCKER_IMAGE}:latest ..."
    $SUDO docker pull ${DOCKER_IMAGE}:latest
    
    log_info "拉取 mongo:7 ..."
    $SUDO docker pull mongo:7
    
    log_info "拉取 redis:7-alpine ..."
    $SUDO docker pull redis:7-alpine
    
    log_success "镜像拉取完成"
}

# 启动服务
start_services() {
    log_step "启动服务"
    
    cd "$INSTALL_DIR"
    $SUDO $DOCKER_COMPOSE up -d
    
    # 等待服务启动
    log_info "等待服务启动..."
    sleep 5
    
    # 检查服务状态
    if $SUDO docker ps | grep -q "kerkerker-douban-service"; then
        log_success "服务启动成功"
    else
        log_error "服务启动失败，请检查日志"
        $SUDO $DOCKER_COMPOSE logs
        exit 1
    fi
}

# 创建管理脚本
create_manage_script() {
    log_step "创建管理脚本"
    
    cat > "$INSTALL_DIR/manage.sh" << 'SCRIPT'
#!/bin/bash

INSTALL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$INSTALL_DIR"

case "$1" in
    start)
        echo "启动服务..."
        docker compose up -d
        ;;
    stop)
        echo "停止服务..."
        docker compose down
        ;;
    restart)
        echo "重启服务..."
        docker compose restart
        ;;
    logs)
        docker compose logs -f ${2:-douban-api}
        ;;
    status)
        docker compose ps
        ;;
    update)
        echo "更新服务..."
        docker compose pull
        docker compose up -d
        echo "更新完成"
        ;;
    config)
        ${EDITOR:-nano} .env
        echo "配置已修改，请运行 '$0 restart' 使配置生效"
        ;;
    uninstall)
        read -p "确定要卸载吗? 这将删除所有数据! [y/N] " -n 1 -r
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            docker compose down -v
            echo "服务已卸载"
        fi
        ;;
    *)
        echo "Kerkerker Douban Service 管理脚本"
        echo ""
        echo "用法: $0 {start|stop|restart|logs|status|update|config|uninstall}"
        echo ""
        echo "命令:"
        echo "  start     启动服务"
        echo "  stop      停止服务"
        echo "  restart   重启服务"
        echo "  logs      查看日志 (可选参数: douban-api|mongo|redis)"
        echo "  status    查看状态"
        echo "  update    更新到最新版本"
        echo "  config    编辑配置文件"
        echo "  uninstall 卸载服务"
        ;;
esac
SCRIPT

    chmod +x "$INSTALL_DIR/manage.sh"
    
    # 创建软链接到 /usr/local/bin
    if [ -d "/usr/local/bin" ]; then
        $SUDO ln -sf "$INSTALL_DIR/manage.sh" /usr/local/bin/douban-service
        log_success "已创建命令别名: douban-service"
    fi
    
    log_success "管理脚本已创建"
}

# 显示完成信息
show_complete() {
    # 获取服务器 IP
    SERVER_IP=$(curl -s ifconfig.me 2>/dev/null || curl -s icanhazip.com 2>/dev/null || echo "your-server-ip")
    
    echo ""
    echo -e "${GREEN}${BOLD}"
    cat << EOF
╔═══════════════════════════════════════════════════════════════╗
║                                                               ║
║   🎉 安装完成!                                                 ║
║                                                               ║
╚═══════════════════════════════════════════════════════════════╝
EOF
    echo -e "${NC}"
    
    echo -e "${CYAN}${BOLD}服务信息:${NC}"
    echo ""
    echo -e "  📍 管理面板:  ${GREEN}http://${SERVER_IP}:${SERVICE_PORT}${NC}"
    echo -e "  📍 API 地址:  ${GREEN}http://${SERVER_IP}:${SERVICE_PORT}/api/v1${NC}"
    echo -e "  📁 安装目录:  ${INSTALL_DIR}"
    echo ""
    
    echo -e "${CYAN}${BOLD}管理命令:${NC}"
    echo ""
    echo "  douban-service start     # 启动服务"
    echo "  douban-service stop      # 停止服务"
    echo "  douban-service restart   # 重启服务"
    echo "  douban-service logs      # 查看日志"
    echo "  douban-service status    # 查看状态"
    echo "  douban-service update    # 更新服务"
    echo "  douban-service config    # 编辑配置"
    echo ""
    
    echo -e "${CYAN}${BOLD}API 端点:${NC}"
    echo ""
    echo "  GET  /api/v1/hero           # Hero Banner"
    echo "  GET  /api/v1/latest         # 最新内容"
    echo "  GET  /api/v1/movies         # 电影分类"
    echo "  GET  /api/v1/tv             # 电视剧分类"
    echo "  GET  /api/v1/new            # 新上线"
    echo "  GET  /api/v1/search?q=关键词 # 搜索"
    echo "  GET  /api/v1/detail/:id     # 详情"
    echo "  GET  /api/v1/category       # 分类分页"
    echo ""
    
    echo -e "${YELLOW}提示: 如果无法访问，请检查防火墙是否开放端口 ${SERVICE_PORT}${NC}"
    echo ""
}

# 主流程
main() {
    show_banner
    
    check_root
    check_system
    check_docker
    check_docker_compose
    create_install_dir
    configure_env
    create_docker_compose
    pull_images
    start_services
    create_manage_script
    
    show_complete
}

# 执行主流程
main
