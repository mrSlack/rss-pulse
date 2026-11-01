FROM golang:1.27.3-alpine3.24

RUN apk add --no-cache ca-certificates
WORKDIR /workspace/backend

COPY go.mod go.sum ./
RUN go mod download
COPY . .

EXPOSE 8080
CMD ["go", "run", "./cmd/api"]
