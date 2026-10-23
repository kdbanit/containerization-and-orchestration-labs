FROM golang:1.27.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /api .

FROM alpine:3.24.3
COPY --from=build /api /api
EXPOSE 8080
ENTRYPOINT ["/api"]
