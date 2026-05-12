FROM python:3.13-slim
WORKDIR /app
COPY renderer/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY renderer/ ./renderer/
COPY hatman.gif .
COPY map.csv .
COPY frames/ ./frames/
COPY textures/ ./textures/
WORKDIR /app/renderer
CMD ["python", "main.py"]

