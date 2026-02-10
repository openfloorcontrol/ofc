FROM python:3.11-slim

# Install system tools
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Install common data science packages
RUN pip install --no-cache-dir \
    pandas \
    numpy \
    matplotlib \
    duckdb \
    scikit-learn

# Set working directory
WORKDIR /workspace

# Keep container running
CMD ["sleep", "infinity"]
