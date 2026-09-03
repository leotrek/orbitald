FROM python:3.12-alpine

WORKDIR /app
ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1
COPY main.py /app/main.py

ENTRYPOINT ["python3", "-I", "/app/main.py"]
