FROM golang:1.27.3-alpine3.24

RUN apk add --no-cache ca-certificates
WORKDIR /workspace/backend

COPY go.mod ./
RUN go mod download
COPY . .

EXPOSE 8080
CMD ["sh", "-c", "go mod tidy && go run ./cmd/api"]
