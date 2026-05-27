FROM alpine:3.22

# 安装 HTTPS 证书与时区数据，保证 S3 HTTPS 请求和时间模板在最小镜像中可用。
RUN echo -e https://mirrors.ustc.edu.cn/alpine/v3.22/main/ > /etc/apk/repositories && \
    apk --no-cache add ca-certificates tzdata && \
    ln -snf /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \
    echo "Asia/Shanghai" > /etc/timezone && \
    mkdir -p /app /data /opt/upimg-defaults

WORKDIR /data

COPY upimg-linux-amd64.tar.gz /tmp/upimg-linux.tar.gz
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

RUN tar -xzf /tmp/upimg-linux.tar.gz -C /app && \
    chmod +x /app/upimg /usr/local/bin/docker-entrypoint.sh && \
    rm -f /tmp/upimg-linux.tar.gz

ENV DATA=/data
ENV FILEPATH=/data/files
ENV PORT=17788

EXPOSE 17788

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
