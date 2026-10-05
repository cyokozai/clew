FROM golang:1.25

WORKDIR /app
ENV CGO_ENABLED=0
ENV GOFLAGS=-mod=mod

# 依存をイメージに焼く。compose の gomod volume が空のとき、初回マウントでこの中身が写される。
# これで network_mode: none の test / vet が新しい環境でも一発で通る。
COPY go.mod go.sum ./
RUN go mod download
