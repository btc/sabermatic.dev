# Stage 1: Build frontend
FROM node:22-alpine AS frontend
WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
RUN npm run build

# Stage 2: Build Go binary
FROM golang:1.25-alpine AS backend
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/web/dist ./web/dist
RUN go build -p 4 -o sabermatic ./cmd/drill

# Stage 3: Runtime
FROM gcr.io/distroless/static-debian12
COPY --from=backend /app/sabermatic /sabermatic
ENTRYPOINT ["/sabermatic"]
