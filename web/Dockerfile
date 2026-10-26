# The web app's builder (docs/web-ui.md): Node and pnpm pinned, so the app builds the same anywhere
# without a local Node. tools/build-web.sh runs it with the repo mounted at /src.
FROM node:18.20.8-bookworm-slim
RUN npm install -g pnpm@8.14.1 && pnpm --version
ENV HOME=/tmp
WORKDIR /src/web
CMD ["sh", "-c", "pnpm install --frozen-lockfile && pnpm build"]
