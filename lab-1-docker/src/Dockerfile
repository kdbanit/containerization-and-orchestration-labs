FROM golang:1.27.1-alpine
WORKDIR /app
COPY api/ .
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/api
ENTRYPOINT ["/app/api"]