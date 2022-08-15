FROM alpine:3.8

RUN apk update upgrade && \
    apk add --no-cache bash util-linux

COPY bootstrap_template.yaml /bootstrap_template.yaml
COPY bootstrap_template_tls.yaml /bootstrap_template_tls.yaml
COPY start.sh /start.sh

RUN ["chmod", "+x", "/start.sh"]
ENTRYPOINT ["/bin/bash", "-c", "/start.sh"]