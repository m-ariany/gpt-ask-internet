# GPT + Internet search

GPT + AskInternet is a free and locally running search aggregator & answer generate using LLM, Without GPU needed. The user can ask a question and the system will use searxng to make a multi engine search and combine the search result to the ChatGPT (3.5, 4) LLM to generate the answer based on search results.

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