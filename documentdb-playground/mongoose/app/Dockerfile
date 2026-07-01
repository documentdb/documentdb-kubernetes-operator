# Mongoose + Express demo for DocumentDB.
FROM node:20-alpine

WORKDIR /app

# Install dependencies first for better layer caching.
COPY package.json package-lock.json ./
RUN npm ci --omit=dev

# Copy application source.
COPY db.js server.js ./
COPY models ./models

# Run as the built-in non-root user.
USER node

ENV PORT=3000
EXPOSE 3000

CMD ["node", "server.js"]
