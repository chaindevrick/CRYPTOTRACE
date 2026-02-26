FROM python:3.11-slim

WORKDIR /app

# 安裝編譯 PostgreSQL 驅動所需的系統依賴
RUN apt-get update && apt-get install -y libpq-dev gcc

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY app.py .

EXPOSE 5000

CMD ["python", "app.py"]