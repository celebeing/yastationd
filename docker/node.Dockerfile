# The web app (web/, embedded in yastationd): Node and pnpm pinned (tools/build.sh web).
FROM node:18.20.8-bookworm-slim
RUN npm install -g pnpm@8.14.1 && pnpm --version
