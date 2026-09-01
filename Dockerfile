# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /out/s3-presigned-url-redirector .

FROM scratch
COPY --from=build /out/s3-presigned-url-redirector /s3-presigned-url-redirector
ENTRYPOINT ["/s3-presigned-url-redirector"]
