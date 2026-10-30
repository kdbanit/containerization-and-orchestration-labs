FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -o /batch .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /batch /batch
ENV CPU_WORKERS=1 MEMORY_MIB=64
ENTRYPOINT ["/batch"]
