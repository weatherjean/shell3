# One-shot Camoufox fetcher for shell3 — anti-detect Firefox in a container.
# Build once:  docker build -t shell3-camoufox ~/.shell3/lib/camoufox
# Run:         docker run --rm shell3-camoufox <url> [css-selector]
FROM python:3.12-slim

# Firefox runtime libraries
RUN apt-get update && apt-get install -y --no-install-recommends \
        libgtk-3-0 libasound2 libx11-xcb1 libdbus-glib-1-2 libxt6 \
    && rm -rf /var/lib/apt/lists/*

RUN pip install --no-cache-dir "camoufox[geoip]"
# Bake the browser + fingerprint data into the image
RUN python -m camoufox fetch

COPY fetch.py /app/fetch.py
ENTRYPOINT ["python", "/app/fetch.py"]
