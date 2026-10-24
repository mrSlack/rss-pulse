FROM node:26.12.1-alpine3.24

WORKDIR /workspace/frontend
RUN apk add --no-cache chromium \
    && npm install --global npm@12.2.1

CMD ["sh", "-c", "npm ci && npm run build:watch"]
