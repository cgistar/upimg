FROM alpine:latest

# 安装 HTTPS 证书与时区数据，保证 S3 HTTPS 请求和时间模板在最小镜像中可用
RUN apk --no-cache add ca-certificates tzdata

RUN mkdir -p /app /data /opt/upimg-defaults

WORKDIR /data

# 使用仓库内 dist 发布包构建镜像；如文件不存在，先执行 ./bin/build.sh linux-amd
COPY upimg-linux-amd64.tar.gz /tmp/upimg-linux-amd64.tar.gz
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

RUN tar -xzf /tmp/upimg-linux-amd64.tar.gz -C /app && \
    chmod +x /app/upimg /usr/local/bin/docker-entrypoint.sh && \
    rm -f /tmp/upimg-linux-amd64.tar.gz

ENV DATA=/data
ENV FILEPATH=/data/files
ENV PORT=17788

EXPOSE 17788

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
