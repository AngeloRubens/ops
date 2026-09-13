# The same application under gunicorn, which is how most of the python world actually deploys it -
# and gunicorn is a master that forks its workers. The pair of them, uvicorn and this, is the fork
# boundary in one ecosystem: same code, same image, one runs and one cannot.
FROM python:3.12-slim
WORKDIR /app
RUN pip install --no-cache-dir fastapi "uvicorn[standard]" gunicorn
COPY app.py .
EXPOSE 8000
CMD ["python", "-m", "gunicorn", "-b", "0.0.0.0:8000", "-k", "uvicorn.workers.UvicornWorker", "app:app"]
