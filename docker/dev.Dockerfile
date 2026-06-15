# Tether reproducible dev environment. See plan.md §1.1.
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

# Base toolchain: Go, Python, build tools.
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl wget git build-essential pkg-config \
        python3 python3-pip python3-venv \
        libasound2-dev \
        unzip zip \
        ninja-build cmake \
    && rm -rf /var/lib/apt/lists/*

# Go 1.22+.
ENV GO_VERSION=1.22.5
RUN curl -fL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" | tar -C /usr/local -xz
ENV PATH=/usr/local/go/bin:/root/go/bin:$PATH
ENV GOPATH=/root/go

# ESP-IDF v5.2 (used by firmware/m5).
ENV IDF_VERSION=v5.2
RUN git clone --depth 1 --branch ${IDF_VERSION} --recursive \
        https://github.com/espressif/esp-idf.git /opt/esp-idf
ENV IDF_PATH=/opt/esp-idf
RUN /opt/esp-idf/install.sh

# PlatformIO core 6+.
RUN pip install --no-cache-dir platformio==6.*

# sherpa-onnx 1.12+ (Parakeet STT).
ENV SHERPA_VERSION=1.12.0
RUN mkdir -p /opt/sherpa-onnx && \
    cd /opt/sherpa-onnx && \
    curl -fL "https://github.com/k2-fsa/sherpa-onnx/releases/download/v${SHERPA_VERSION}/sherpa-onnx-v${SHERPA_VERSION}-linux-x64-shared.tar.bz2" \
        -o sherpa.tar.bz2 && \
    tar -xjf sherpa.tar.bz2 && \
    rm sherpa.tar.bz2

# piper1-gpl 1.x (TTS).
ENV PIPER_VERSION=1.2.0
RUN mkdir -p /opt/piper && \
    cd /opt/piper && \
    curl -fL "https://github.com/OHF-Voice/piper1-gpl/releases/download/v${PIPER_VERSION}/piper_linux_x86_64.tar.gz" \
        -o piper.tar.gz && \
    tar -xzf piper.tar.gz && \
    rm piper.tar.gz

# protoc 25+.
ENV PROTOC_VERSION=25.1
RUN mkdir -p /opt/protoc && \
    cd /opt/protoc && \
    curl -fL "https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/protoc-${PROTOC_VERSION}-linux-x86_64.zip" \
        -o protoc.zip && \
    unzip protoc.zip && \
    rm protoc.zip
ENV PATH=/opt/protoc/bin:/root/go/bin:$PATH

# Go protobuf generator.
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest

# clang-format (for C++ formatting gate).
RUN apt-get update && apt-get install -y --no-install-recommends clang-format && rm -rf /var/lib/apt/lists/*

# golangci-lint (latest).
RUN curl -fL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | \
        sh -s -- -b /root/go/bin

WORKDIR /src
CMD ["/bin/bash"]
