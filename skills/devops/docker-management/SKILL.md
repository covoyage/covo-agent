---
name: docker-management
description: "Docker lifecycle: containers, images, volumes, networks, and Compose stacks."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [docker]
metadata:
  tags: [docker, containers, images, compose, devops, debugging]
  related_skills: [process]
---

# Docker Management

Manage Docker containers, images, volumes, networks, and Compose stacks.

## Containers

```bash
# List running
docker ps
docker ps -a  # all

# Start/stop/restart
docker start <name>
docker stop <name>
docker restart <name>

# Logs
docker logs <name>
docker logs -f <name>       # follow
docker logs --tail 50 <name> # last 50 lines

# Inspect
docker inspect <name>
docker stats <name>          # live resource usage

# Execute inside
docker exec -it <name> bash
docker exec <name> ls /app

# Cleanup
docker rm <name>
docker rm $(docker ps -aq)   # all stopped
docker container prune -f
```

## Images

```bash
docker images
docker pull <image>:<tag>
docker build -t <name> .
docker tag <src> <dst>
docker push <image>
docker rmi <image>
docker image prune -f
```

## Volumes & Networks

```bash
docker volume ls
docker volume inspect <name>
docker volume prune -f

docker network ls
docker network inspect <name>
docker network create <name>
docker network connect <net> <container>
```

## Docker Compose

```bash
docker compose up -d
docker compose down
docker compose restart
docker compose logs -f
docker compose ps
docker compose exec <service> bash
docker compose build --no-cache
```

## Debugging

```bash
# Why won't it start?
docker logs <name> 2>&1 | tail -50
docker inspect <name> | jq '.[0].State'

# Resource issues
docker stats --no-stream
docker system df

# Network debugging
docker run --rm -it alpine ping <host>
docker run --rm -it alpine nslookup <host>
docker run --rm -it curlimages/curl <url>

# Clean everything
docker system prune -af --volumes
```

## Dockerfile Best Practices

```dockerfile
FROM python:3.12-slim
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY . .
USER 1000:1000
CMD ["python", "main.py"]
```
