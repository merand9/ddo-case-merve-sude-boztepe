# syntax=docker/dockerfile:1.7
# Aşama 1: React (Vite + TypeScript) derlemesi. Tip denetimi derlemenin parçasıdır.
FROM node:22-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY tsconfig.json vite.config.ts index.html ./
COPY public ./public
COPY src ./src
RUN npm run build && find dist -type f \( -name '*.js' -o -name '*.css' -o -name '*.html' -o -name '*.svg' \) -exec gzip -k -9 {} \;

# Aşama 2: root olmayan nginx (uid 101), 8080'de dinler; yalnızca derlenmiş statik dosyalar.
FROM nginxinc/nginx-unprivileged:1.27-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 8080
