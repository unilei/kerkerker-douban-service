#!/bin/bash

# =============================================================================
# Docker Hub 镜像打包上传脚本
# 用法: ./scripts/docker-push.sh [版本号]
# 示例: ./scripts/docker-push.sh 1.0.0
# =============================================================================

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 配置
DOCKER_USERNAME="${DOCKER_USERNAME:-}"
IMAGE_NAME="kerkerker-douban-service"
VERSION="${1:-latest}"

# 打印带颜色的消息
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# 显示帮助
show_help() {
    echo "用法: $0 [选项] [版本号]"
    echo ""
    echo "选项:"
    echo "  -h, --help     显示帮助信息"
    echo "  -u, --user     Docker Hub 用户名"
    echo "  -n, --name     镜像名称 (默认: kerkerker-douban-service)"
    echo ""
    echo "示例:"
    echo "  $0 1.0.0                              # 使用默认用户名推送 v1.0.0"
    echo "  $0 -u myuser 1.0.0                    # 指定用户名推送 v1.0.0"
    echo "  $0 -u myuser -n my-service latest     # 自定义镜像名称"
    echo ""
    echo "环境变量:"
    echo "  DOCKER_USERNAME    Docker Hub 用户名"
    echo ""
}

# 解析参数
while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help)
            show_help
            exit 0
            ;;
        -u|--user)
            DOCKER_USERNAME="$2"
            shift 2
            ;;
        -n|--name)
            IMAGE_NAME="$2"
            shift 2
            ;;
        *)
            VERSION="$1"
            shift
            ;;
    esac
done

# 检查 Docker 是否运行
check_docker() {
    if ! docker info > /dev/null 2>&1; then
        log_error "Docker 未运行，请先启动 Docker"
        exit 1
    fi
    log_success "Docker 正在运行"
}

# 检查 Docker Hub 登录状态
check_login() {
    if [ -z "$DOCKER_USERNAME" ]; then
        log_warn "未指定 Docker Hub 用户名"
        read -p "请输入 Docker Hub 用户名: " DOCKER_USERNAME
        if [ -z "$DOCKER_USERNAME" ]; then
            log_error "用户名不能为空"
            exit 1
        fi
    fi

    log_info "检查 Docker Hub 登录状态..."
    if ! docker info 2>/dev/null | grep -q "Username"; then
        log_warn "未登录 Docker Hub，正在登录..."
        docker login
    fi
    log_success "Docker Hub 登录成功"
}

# 构建镜像
build_image() {
    local full_image_name="${DOCKER_USERNAME}/${IMAGE_NAME}"
    
    log_info "开始构建镜像: ${full_image_name}:${VERSION}"
    
    # 获取脚本所在目录的上级目录（项目根目录）
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
    
    cd "$PROJECT_DIR"
    
    # 构建多平台镜像
    log_info "构建镜像中..."
    docker build \
        --platform linux/amd64,linux/arm64 \
        -t "${full_image_name}:${VERSION}" \
        -t "${full_image_name}:latest" \
        . 2>&1 | while read line; do echo "  $line"; done
    
    log_success "镜像构建完成"
}

# 推送镜像
push_image() {
    local full_image_name="${DOCKER_USERNAME}/${IMAGE_NAME}"
    
    log_info "推送镜像到 Docker Hub..."
    
    # 推送指定版本
    log_info "推送 ${full_image_name}:${VERSION}"
    docker push "${full_image_name}:${VERSION}"
    
    # 如果不是 latest，也推送 latest 标签
    if [ "$VERSION" != "latest" ]; then
        log_info "推送 ${full_image_name}:latest"
        docker push "${full_image_name}:latest"
    fi
    
    log_success "镜像推送完成"
}

# 使用 buildx 构建和推送多平台镜像
build_and_push_multiplatform() {
    local full_image_name="${DOCKER_USERNAME}/${IMAGE_NAME}"
    
    log_info "使用 buildx 构建多平台镜像..."
    
    # 获取项目目录
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
    cd "$PROJECT_DIR"
    
    # 创建/使用 buildx builder
    if ! docker buildx inspect multiarch > /dev/null 2>&1; then
        log_info "创建 buildx builder..."
        docker buildx create --name multiarch --use
    else
        docker buildx use multiarch
    fi
    
    # 启动 builder
    docker buildx inspect --bootstrap > /dev/null 2>&1
    
    # 构建并推送
    log_info "构建并推送多平台镜像 (linux/amd64, linux/arm64)..."
    
    local tags="-t ${full_image_name}:${VERSION}"
    if [ "$VERSION" != "latest" ]; then
        tags="$tags -t ${full_image_name}:latest"
    fi
    
    docker buildx build \
        --platform linux/amd64,linux/arm64 \
        $tags \
        --push \
        .
    
    log_success "多平台镜像构建并推送完成"
}

# 显示结果
show_result() {
    local full_image_name="${DOCKER_USERNAME}/${IMAGE_NAME}"
    
    echo ""
    echo "=============================================="
    log_success "🎉 镜像推送成功!"
    echo "=============================================="
    echo ""
    echo "镜像地址:"
    echo "  docker pull ${full_image_name}:${VERSION}"
    if [ "$VERSION" != "latest" ]; then
        echo "  docker pull ${full_image_name}:latest"
    fi
    echo ""
    echo "Docker Hub 页面:"
    echo "  https://hub.docker.com/r/${DOCKER_USERNAME}/${IMAGE_NAME}"
    echo ""
}

# 主流程
main() {
    echo ""
    echo "=============================================="
    echo "  🐳 Docker Hub 镜像打包上传工具"
    echo "=============================================="
    echo ""
    
    check_docker
    check_login
    
    echo ""
    log_info "镜像名称: ${DOCKER_USERNAME}/${IMAGE_NAME}"
    log_info "版本号: ${VERSION}"
    echo ""
    
    # 询问是否使用多平台构建
    read -p "是否构建多平台镜像 (amd64/arm64)? [Y/n] " multiplatform
    multiplatform=${multiplatform:-Y}
    
    if [[ "$multiplatform" =~ ^[Yy]$ ]]; then
        build_and_push_multiplatform
    else
        build_image
        push_image
    fi
    
    show_result
}

# 执行主流程
main
