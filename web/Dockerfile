# syntax=docker/dockerfile:1.7
# Aşama 1: statik dosyaları sıkıştır (gzip_static için .gz üretir).
FROM alpine:3.20 AS assets
WORKDIR /site
COPY index.html app.js app.css ./
RUN gzip -k -9 app.js app.css index.html

# Aşama 2: root olmayan nginx (uid 101), 8080'de dinler.
FROM nginxinc/nginx-unprivileged:1.27-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=assets /site /usr/share/nginx/html
EXPOSE 8080
