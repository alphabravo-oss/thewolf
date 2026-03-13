# Sample Dockerfile with known issues for hadolint testing

FROM ubuntu:latest

# BAD: Using root user
# BAD: No pinned version for apt packages
RUN apt-get update && apt-get install -y curl wget python3

# BAD: Using ADD instead of COPY for local files
ADD ./app /app

# BAD: No WORKDIR set
RUN cd /app && python3 setup.py install

EXPOSE 8080

CMD ["python3", "/app/main.py"]
