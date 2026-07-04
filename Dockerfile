FROM node:24-alpine AS web-builder
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY index.html vite.config.ts tsconfig*.json ./
COPY public ./public
COPY src ./src
RUN npm run build

FROM golang:1.26-alpine AS api-builder
WORKDIR /app
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/catieapi ./cmd/catieapi

FROM alpine:3.22
WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata
COPY --from=api-builder /out/catieapi /app/catieapi
COPY --from=web-builder /app/dist /app/dist
ENV PORT=8787
ENV STATIC_DIR=/app/dist
ENV PERSISTENCE=postgres
EXPOSE 8787
CMD ["/app/catieapi"]
