FROM  cargo.caicloud.xyz/library/debian:stretch

LABEL maintainer="Huanle Han <han.huanle@caicloud.io>"

WORKDIR /root

COPY bin/goben /root/goben

ENTRYPOINT ["/root/goben"]

EXPOSE 8080
