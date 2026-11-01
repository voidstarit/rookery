FROM debian:trixie-slim

RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        bc \
        bison \
        busybox-static \
        ca-certificates \
        cpio \
        curl \
        diffutils \
        flex \
        gcc \
        gzip \
        libc6-dev \
        libelf-dev \
        libncurses-dev \
        libssl-dev \
        make \
        perl \
        xz-utils \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /work
