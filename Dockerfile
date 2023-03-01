# ================================================
# | NPM BUILDER - FRONTEND
# ================================================

# Use an official Node runtime as a parent image
FROM node:14 AS npm_builder

# Set the working directory to /app
WORKDIR /app
# Copy the package.json and package-lock.json file to the container
COPY ./frontend/package*.json ./
# Install dependencies
RUN npm install
# Copy the rest of the application code to the container
COPY ./frontend .
# Build the React app
RUN npm run build

# ================================================
# | GO BUILDER - BACKEND
# ================================================

# Use an official Golang runtime as a parent image
FROM golang:1.16

RUN apt-get update && apt-get install -y libwebp-dev  ffmpeg webp

# Set the working directory to /app
WORKDIR /app
# Copy the output of the React build to the Go project
COPY --from=npm_builder /app/build ./public
# Copy the rest of the Go application code to the container
COPY ./backend .
RUN mkdir -p ./tmp/thumbnails
# Build the Go project
RUN go build -o ginbar .
