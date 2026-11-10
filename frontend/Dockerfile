FROM node:24-alpine AS build
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
RUN npm run build -- --configuration production \
    && mkdir /out \
    && cp -r "$(dirname "$(find dist -name index.html | head -1)")"/. /out/

FROM nginxinc/nginx-unprivileged:1.27-alpine
# The SPA calls relative /api/* paths, so nginx proxies them to the backend.
# The template is rendered by the image entrypoint (envsubst) into conf.d/default.conf at startup.
ENV API_UPSTREAM=http://api:8080
COPY nginx.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build /out/ /usr/share/nginx/html/
USER 101
EXPOSE 8080
