FROM alpine:3.8

RUN apk update upgrade && \
    apk add --no-cache bash util-linux

COPY bootstrap_template.yaml /bootstrap_template.yaml
COPY start.sh /start.sh

ENTRYPOINT ["/bin/bash", "-c", "/start.sh"]