# Intentionally vulnerable training fixture. Do not build or deploy.
FROM ubuntu:latest
RUN apt-get update && apt-get install -y curl
COPY . /sample
WORKDIR /sample
CMD ["echo", "Wolf training sample only"]
