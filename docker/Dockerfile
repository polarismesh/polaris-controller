FROM alpine:latest
RUN apk update \
    && apk add tzdata \
    && cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime \
    && echo "Asia/Shanghai" > /etc/timezone
RUN mkdir -p /polaris-controller/log && \
    chmod -R 755 /polaris-controller

WORKDIR /polaris-controller
COPY ../bin/polaris-controller /polaris-controller/polaris-controller

ENTRYPOINT ["/polaris-controller/polaris-controller"]
