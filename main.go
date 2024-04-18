package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	gpt "github.com/m-ariany/gpt-chat-client"
	"github.com/markusmobius/go-trafilatura"
	openai "github.com/sashabaranov/go-openai"
)

var (
	httpClient = &http.Client{Timeout: 30 * time.Second}
)

type SearchResult struct {
	SiteName string `json:"site_name"`
	IconURL  string `json:"icon_url"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Snippet  string `json:"snippet"`
}

type ContentResult struct {
	URL     string `json:"url"`
	Content string `json:"content"`
	Length  int    `json:"length"`
}

type EmbeddingsResult struct {
	Content         string
	Embeddings      []float32
	SimilarityScore float64
}

func extractUrlContent(ctx context.Context, urlStr string) (*ContentResult, error) {

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	parsedURL, err := url.ParseRequestURI(urlStr)
	if err != nil {
		fmt.Println("failed to parse url: %v", err)
		return nil, err
	}

	// Fetch article
	resp, err := httpClient.Get(urlStr)
	if err != nil {
		fmt.Println("failed to fetch the page: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	// Extract content
	opts := trafilatura.Options{
		OriginalURL: parsedURL,
	}

	result, err := trafilatura.Extract(resp.Body, opts)
	if err != nil {
		fmt.Println("failed to extract: %v", err)
		return nil, err
	}

	return &ContentResult{
		URL:     urlStr,
		Content: result.ContentText,
		Length:  len(result.ContentText),
	}, nil
}

func searchWebRef(ctx context.Context, query string) ([]ContentResult, error) {
	safeString := url.QueryEscape(":all !general " + query)
	resp, err := http.Get(fmt.Sprintf("http://localhost:8080?q=%s&format=json", safeString))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var searchData struct {
		Results []SearchResult `json:"results"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&searchData); err != nil {
		return nil, err
	}

	firstNRusults := 10
	if len(searchData.Results) < 10 {
		firstNRusults = len(searchData.Results)
	}

	var wg sync.WaitGroup
	contentsChan := make(chan *ContentResult, firstNRusults)
	for _, item := range searchData.Results[:firstNRusults] {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			content, err := extractUrlContent(ctx, url)
			if err == nil {
				contentsChan <- content
			}
		}(item.URL)
	}
	wg.Wait()
	close(contentsChan)

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	var contents []ContentResult
	for content := range contentsChan {
		if content != nil && content.Content != "" {
			contents = append(contents, *content)
		}
	}

	return contents, nil
}

func genInstructionAndContext(embeddingsInput []EmbeddingsResult) ([]openai.ChatCompletionMessage, error) {

	var refContent []string
	for _, item := range embeddingsInput {
		refContent = append(refContent, item.Content)
	}

	messages := []openai.ChatCompletionMessage{}

	if len(refContent) > 0 {

		instruction := `
		As an AI assistant, your role is to answer user questions using the provided context by the assistant. 
            
		When answering, adhere to the following guidelines to ensure your response is clear, concise, and accurate:
		- Use the provided context related to the question.
		- Your response should be factual, precise, and exhibit expertise, maintaining an unbiased and professional tone throughout.
		- Limit your answer to maximum 1024 words to maintain conciseness.
		- If the provided context lacks sufficient information on a topic, indicate this by stating "information is missing."
		- Except for code snippets, specific names, and citations, compose your response in the same language as the posed question.
		- Ensure to review the provided context before crafting your answer.
		`

		messages = append(messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: instruction,
		})

		var context string
		for _, refText := range refContent {
			context += fmt.Sprintf("%s \n", refText)
		}

		messages = append(messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleAssistant,
			Content: context,
		})

	} else {
		instruction := `
		As an AI assistant, your role is to answer user questions. 
            
		When answering, adhere to the following guidelines to ensure your response is clear, concise, and accurate:
		- Your response should be factual, precise, and exhibit expertise, maintaining an unbiased and professional tone throughout.
		- Limit your answer to maximum 1024 words to maintain conciseness.
		- If you are unsure of the answer or lack sufficient information to respond accurately, simply state, "I do not know."
		- Except for code snippets, specific names, and citations, compose your response in the same language as the posed question.
		`

		messages = append(messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: instruction,
		})
	}

	return messages, nil
}

func genEmbeddings(ctx context.Context, input []string) (openai.EmbeddingResponse, error) {
	os.Getenv("GILAS_API_KEY")
	clientConfig := openai.DefaultConfig(os.Getenv("GILAS_API_KEY"))
	clientConfig.BaseURL = os.Getenv("GILAS_API_URL")
	client := openai.NewClientWithConfig(clientConfig)
	return client.CreateEmbeddings(ctx, openai.EmbeddingRequestStrings{
		Input: input,
		Model: openai.LargeEmbedding3,
	})
}

func getEmbeddings(ctx context.Context, content []ContentResult) ([]EmbeddingsResult, error) {

	// max dimension: https://platform.openai.com/docs/api-reference/embeddings/create#embeddings-create-input
	maxDimension := 2048
	embeddingsInput := []string{}
	index := 0
	for _, c := range content {
		if index > maxDimension {
			break
		}
		// Maximum length for each dimension in character
		maxLength := 1500 // it is about 500 tokens
		input := c.Content
		// slice the input to smaller chunks
		for len(input) > 0 {
			// Determine the substring length
			subLength := maxLength
			if len(input) < maxLength {
				subLength = len(input)
			}

			// Ensure we do not cut off a multi-byte character
			if subLength < len(input) {
				for !utf8.ValidString(input[:subLength]) {
					subLength--
				}
			}

			// Extract the substring
			substring := input[:subLength]
			substring = strings.ReplaceAll(substring, "\n", " ")

			// Reduce the input string
			input = input[subLength:]

			embeddingsInput = append(embeddingsInput, substring)
			index++
			if index > maxDimension {
				break
			}
		}
	}

	resp, err := genEmbeddings(ctx, embeddingsInput)

	if err != nil {
		fmt.Println("failed to generate embeddings: %v", err)
		return nil, err
	}

	result := []EmbeddingsResult{}
	for i, data := range resp.Data {
		result = append(result, EmbeddingsResult{
			Content:    embeddingsInput[i],
			Embeddings: data.Embedding,
		})
	}

	return result, nil
}

// ref: https://github.com/gaspiman/cosine_similarity/blob/master/cosine.go
func cosine_similarity(a []float32, b []float32) (cosine float64, err error) {
	count := 0
	length_a := len(a)
	length_b := len(b)
	if length_a > length_b {
		count = length_a
	} else {
		count = length_b
	}
	sumA := 0.0
	s1 := 0.0
	s2 := 0.0
	for k := 0; k < count; k++ {
		if k >= length_a {
			s2 += math.Pow(float64(b[k]), 2)
			continue
		}
		if k >= length_b {
			s1 += math.Pow(float64(a[k]), 2)
			continue
		}
		sumA += float64(a[k] * b[k])
		s1 += math.Pow(float64(a[k]), 2)
		s2 += math.Pow(float64(b[k]), 2)
	}
	if s1 == 0 || s2 == 0 {
		return 0.0, errors.New("Vectors should not be null (all zeros)")
	}
	return sumA / (math.Sqrt(s1) * math.Sqrt(s2)), nil
}

func findRelevantResults(ctx context.Context, query string, embeddings []EmbeddingsResult, n int) []EmbeddingsResult {

	if len(embeddings) < n {
		n = len(embeddings)
	}

	resp, err := genEmbeddings(ctx, []string{query})
	if err != nil {
		panic(err)
	}

	for _, e := range embeddings {
		score, err := cosine_similarity(resp.Data[0].Embedding, e.Embeddings)
		if err != nil {
			panic(err)
		}
		e.SimilarityScore = score
	}

	// sort the embeddings according to their similarity score descending
	sort.Slice(embeddings, func(i, j int) bool {
		return embeddings[i].SimilarityScore > embeddings[j].SimilarityScore
	})

	return embeddings[:n]
}

func askInternet(ctx context.Context, query string) {
	var temperature float32 = 0.3
	gptClient, err := gpt.NewClient(gpt.ClientConfig{
		ApiUrl:      os.Getenv("GILAS_API_URL"),
		ApiKey:      os.Getenv("GILAS_API_KEY"),
		ApiTimeout:  time.Minute * 2,
		Model:       "gpt-4-turbo",
		Temperature: &temperature,
	})
	if err != nil {
		panic(err)
	}

	gptClient.Instruct("Create a precise and short search term for the given user input")
	query, _ = gptClient.Prompt(ctx, query)
	fmt.Println("query:", query)

	content, err := searchWebRef(ctx, query)
	if err != nil {
		panic(err)
	}

	embeddings, err := getEmbeddings(ctx, content)
	if err != nil {
		panic(err)
	}

	// select the relevant emebeddings
	relevantEmbeddings := findRelevantResults(ctx, query, embeddings, 10)

	// use the relevant embeddings
	messages, err := genInstructionAndContext(relevantEmbeddings)
	if err != nil {
		panic(err)
	}

	gptClient = gptClient.Clone()
	gptClient.ImportHistory(messages)
	replyMessage, err := gptClient.Prompt(ctx, query)
	if err != nil {
		panic(err)
	}

	fmt.Println(replyMessage)

	h, _ := gptClient.ExportHistory().ToString()
	fmt.Println("context length:", len(h))

	var s string
	for _, c := range content {
		s += c.Content
	}
	fmt.Println("web result length:", len(s))
}

func main() {
	// Example usage
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	askInternet(ctx, "What are the 10 must visited in Singapur? Please consider that we have a 17 month old child")
}
