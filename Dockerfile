FROM golang:1.23-bullseye AS gobuild
ENV GOPROXY='https://goproxy.cn,direct'
WORKDIR /build
ADD . /build/
RUN go mod download -x
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o ./csi-3fs ./cmd/csi-driver-3fs

FROM alpine:3.20
RUN apk add --no-cache fuse
COPY --from=gobuild /build/csi-3fs /csi-3fs
ENTRYPOINT ["/csi-3fs"]