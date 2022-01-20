FROM alpine:latest
RUN apk update \
    && apk add tzdata \
    && apk add --no-cache bash \
    && apk add curl \
    && cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime \
    && echo "Asia/Shanghai" > /etc/timezone

RUN mkdir -p /polaris-controller/log && \
    chmod -R 755 /polaris-controller

WORKDIR /polaris-controller
COPY polaris-controller /polaris-controller/polaris-controller