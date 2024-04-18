# GPT + Internet search

GPT + AskInternet is a GPU-free, locally-operating search aggregator and answer generator. It utilizes searxng for multi-engine searches and ChatGPT 4 to produce answers based on combined search results.

It works very similar to the https://www.perplexity.ai/ but without any limitation.

## How to run

First:

```
docker run --restart=always -it -p 8080:8080\
  -v $(pwd)/searxng:/etc/searxng:rw \
  -e SEARXNG_BASE_URL=https://${SEARXNG_HOSTNAME:-localhost}/ \
  --cap-drop=ALL \
  --cap-add=CHOWN \
  --cap-add=SETGID \
  --cap-add=SETUID \
  --log-driver=json-file \
  --log-opt max-size=1m \
  --log-opt max-file=1 \
  docker.io/searxng/searxng:latest
```

and then:

```
export GILAS_API_URL=https://api.gilas.io/v1
export GILAS_API_KEY=XXX

go run main.go
```
