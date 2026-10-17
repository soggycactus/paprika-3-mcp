package paprika

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// roundTripper is a wrapper around http.RoundTripper
// that adds the specified headers to each request
type roundTripper struct {
	headers   map[string]string
	transport http.RoundTripper
}

func (r *roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range r.headers {
		// Only set if not already present
		if req.Header.Get(k) == "" {
			req.Header.Set(k, v)
		}
	}
	return r.transport.RoundTrip(req)
}

// retryTransport wraps an http.RoundTripper with retry logic for 429 and 5xx responses.
// It uses exponential backoff and respects the Retry-After header.
type retryTransport struct {
	transport  http.RoundTripper
	maxRetries int
	baseDelay  time.Duration
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Read body upfront so we can replay it on retries
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read request body: %w", err)
		}
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}

	var resp *http.Response
	var err error

	for attempt := 0; attempt <= t.maxRetries; attempt++ {
		if bodyBytes != nil && attempt > 0 {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		resp, err = t.transport.RoundTrip(req)
		if err != nil {
			// Retry network errors (connection resets, header timeouts):
			// paprikaapp.com drops connections intermittently. Sync writes
			// upsert by UID, so replaying a POST is safe.
			if attempt == t.maxRetries {
				return nil, err
			}
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(t.baseDelay * (1 << attempt)):
			}
			continue
		}

		// Don't retry on success or client errors (except 429)
		if resp.StatusCode < 429 || (resp.StatusCode > 429 && resp.StatusCode < 500) {
			return resp, nil
		}

		// Last attempt — return whatever we got
		if attempt == t.maxRetries {
			return resp, nil
		}

		// Calculate delay: use Retry-After header if present, otherwise exponential backoff
		delay := t.baseDelay * (1 << attempt)
		if resp.StatusCode == 429 {
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if seconds, parseErr := time.ParseDuration(ra + "s"); parseErr == nil {
					delay = seconds
				}
			}
		}

		// Drain and close the response body before retrying
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(delay):
		}
	}

	return resp, nil
}

func userAgent(version string) string {
	return fmt.Sprintf("paprika-3-mcp/%s (golang; %s)", version, runtime.Version())
}

func NewClient(username, password, version string, logger *slog.Logger) (*Client, error) {
	// Create the http client & login to retrieve an authentication token
	t := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := &net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}
			return d.DialContext(ctx, network, addr)
		},
		ResponseHeaderTimeout: 10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	client := &http.Client{
		Transport: t,
		Timeout:   10 * time.Second,
	}

	// Retry login: paprikaapp.com intermittently resets connections.
	var token string
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		token, err = login(ctx, *client, username, password)
		cancel()
		if err == nil {
			break
		}
		logger.Warn("login attempt failed", "attempt", attempt, "err", err)
		if attempt < 3 {
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to login: %w", err)
	}

	client.Transport = &retryTransport{
		maxRetries: 3,
		baseDelay:  500 * time.Millisecond,
		transport: &roundTripper{
			transport: t,
			headers: map[string]string{
				"Accept":        "*/*",
				"Authorization": fmt.Sprintf("Bearer %s", token),
				"Connection":    "keep-alive",
				"User-Agent":    userAgent(version),
			},
		},
	}

	l := logger
	if l == nil {
		l = slog.Default()
	}

	return &Client{
		client:   client,
		logger:   l,
		username: username,
		password: password,
	}, nil
}

type Client struct {
	client   *http.Client
	logger   *slog.Logger
	username string
	password string
}

type loginResponse struct {
	Result struct {
		Token string `json:"token"`
	} `json:"result"`
}

type errorResponse struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// login authenticates with the Paprika API and returns an authentication token
// The token is used for all subsequent requests to the API. As far as I can tell, this is a JWT with no expiration.
func login(ctx context.Context, client http.Client, username, password string) (string, error) {
	body := fmt.Sprintf("email=%s&password=%s", username, password)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://paprikaapp.com/api/v1/account/login", bytes.NewBufferString(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to login: %s", resp.Status)
	}

	rawBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var loginResp loginResponse
	if err := json.Unmarshal(rawBytes, &loginResp); err != nil {
		return "", err
	}

	if loginResp.Result.Token == "" {
		return "", fmt.Errorf("failed to get token: %s", string(rawBytes))
	}

	return loginResp.Result.Token, nil
}

type RecipeList struct {
	Result []struct {
		UID  string `json:"uid"`
		Hash string `json:"hash"`
	} `json:"result"`
}

// ListRecipes retrieves a list of recipes from the Paprika API - the response objects
// only contain the UID and hash of each recipe, not the full recipe object
func (c *Client) ListRecipes(ctx context.Context) (*RecipeList, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://paprikaapp.com/api/v2/sync/recipes", nil)
	if err != nil {
		c.logger.Error("failed to create request", "error", err)
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		c.logger.Error("failed to get recipes", "error", err)
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.logger.Error("failed to get recipes", "status", resp.Status)
		return nil, fmt.Errorf("failed to get recipes: %s", resp.Status)
	}

	rawBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logger.Error("failed to read response body", "error", err)
		return nil, err
	}
	var recipeList RecipeList
	if err := json.Unmarshal(rawBytes, &recipeList); err != nil {
		c.logger.Error("failed to unmarshal response", "error", err)
		return nil, err
	}

	c.logger.Info("found recipes", "count", len(recipeList.Result))
	return &recipeList, nil
}

const (
	MealTypeBreakfast = 0
	MealTypeLunch     = 1
	MealTypeDinner    = 2
)

// MealTypeName returns a human-readable name for a meal type constant.
func MealTypeName(t int) string {
	switch t {
	case MealTypeBreakfast:
		return "Breakfast"
	case MealTypeLunch:
		return "Lunch"
	case MealTypeDinner:
		return "Dinner"
	default:
		return "Meal"
	}
}

type MealPlan struct {
	UID       string `json:"uid"`
	RecipeUID string `json:"recipe_uid"`
	Date      string `json:"date"`
	Type      int    `json:"type"`
	Name      string `json:"name"`
	OrderFlag int    `json:"order_flag"`
	Deleted   bool   `json:"deleted"`
}

type MealPlanResponse struct {
	Result []MealPlan `json:"result"`
}

type GroceryItem struct {
	UID         string `json:"uid"`
	RecipeUID   string `json:"recipe_uid"`
	Name        string `json:"name"`
	OrderFlag   int    `json:"order_flag"`
	Purchased   bool   `json:"purchased"`
	Aisle       string `json:"aisle"`
	Ingredient  string `json:"ingredient"`
	Recipe      string `json:"recipe"`
	Instruction string `json:"instruction"`
	Quantity    string `json:"quantity"`
	AisleUID    string `json:"aisle_uid"`
	ListUID     string `json:"list_uid"`
	Deleted     bool   `json:"deleted"` // Soft delete flag (like MealPlan)
}

type GroceryResponse struct {
	Result []GroceryItem `json:"result"`
}

type Recipe struct {
	UID             string   `json:"uid"`
	Name            string   `json:"name"`
	Ingredients     string   `json:"ingredients"`
	Directions      string   `json:"directions"`
	Description     string   `json:"description"`
	Notes           string   `json:"notes"`
	NutritionalInfo string   `json:"nutritional_info"`
	Servings        string   `json:"servings"`
	Difficulty      string   `json:"difficulty"`
	PrepTime        string   `json:"prep_time"`
	CookTime        string   `json:"cook_time"`
	TotalTime       string   `json:"total_time"`
	Source          string   `json:"source"`
	SourceURL       string   `json:"source_url"`
	ImageURL        string   `json:"image_url"`
	Photo           string   `json:"photo"`
	PhotoHash       string   `json:"photo_hash"`
	PhotoLarge      string   `json:"photo_large"`
	Scale           string   `json:"scale"`
	Hash            string   `json:"hash"`
	Categories      []string `json:"categories"`
	Rating          int      `json:"rating"`
	InTrash         bool     `json:"in_trash"`
	IsPinned        bool     `json:"is_pinned"`
	OnFavorites     bool     `json:"on_favorites"`
	OnGroceryList   bool     `json:"on_grocery_list"`
	Created         string   `json:"created"`
	PhotoURL        string   `json:"photo_url"`
}

func (r *Recipe) ResourceDescription() string {
	if len(r.Description) == 0 {
		return fmt.Sprintf("A recipe for %s", r.Name)
	}

	return fmt.Sprintf("A recipe for %s: %s", r.Name, r.Description)
}

func (r *Recipe) ToMarkdown() string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# %s\n\n", r.Name))

	if r.Description != "" {
		sb.WriteString(fmt.Sprintf("_%s_\n\n", r.Description))
	}

	if r.Servings != "" || r.PrepTime != "" || r.CookTime != "" || r.Difficulty != "" {
		sb.WriteString("## Details\n")
		if r.Servings != "" {
			sb.WriteString(fmt.Sprintf("- **Servings:** %s\n", r.Servings))
		}
		if r.PrepTime != "" {
			sb.WriteString(fmt.Sprintf("- **Prep Time:** %s\n", r.PrepTime))
		}
		if r.CookTime != "" {
			sb.WriteString(fmt.Sprintf("- **Cook Time:** %s\n", r.CookTime))
		}
		if r.Difficulty != "" {
			sb.WriteString(fmt.Sprintf("- **Difficulty:** %s\n", r.Difficulty))
		}
		sb.WriteString("\n")
	}

	if r.Ingredients != "" {
		sb.WriteString("## Ingredients\n")
		for _, line := range strings.Split(strings.TrimSpace(r.Ingredients), "\n") {
			if line != "" {
				sb.WriteString(fmt.Sprintf("- %s\n", line))
			}
		}
		sb.WriteString("\n")
	}

	if r.Directions != "" {
		sb.WriteString("## Directions\n")
		lines := strings.Split(strings.TrimSpace(r.Directions), "\n")
		for i, line := range lines {
			if line != "" {
				sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, line))
			}
		}
		sb.WriteString("\n")
	}

	if r.Notes != "" {
		sb.WriteString("## Notes\n")
		sb.WriteString(r.Notes + "\n\n")
	}

	return sb.String()
}

func (r *Recipe) generateUUID() {
	// Generate a new UUID for the recipe
	if r.UID == "" {
		r.UID = strings.ToUpper(uuid.New().String())
		return
	}

	r.UID = strings.ToUpper(r.UID)
}

func (r *Recipe) updateCreated() {
	layout := "2006-01-02 15:04:05"
	r.Created = time.Now().Format(layout)
}

func (r *Recipe) asMap() (map[string]interface{}, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}

	var fields map[string]interface{}
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}

	return fields, nil
}

func (r *Recipe) updateHash() error {
	fields, err := r.asMap()
	if err != nil {
		return err
	}

	// Remove the "hash" field
	delete(fields, "hash")

	// Sort keys manually to ensure consistent JSON output
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build a sorted map for consistent hashing
	sorted := make(map[string]interface{}, len(fields))
	for _, k := range keys {
		sorted[k] = fields[k]
	}

	// Marshal the sorted map to JSON
	jsonBytes, err := json.Marshal(sorted)
	if err != nil {
		return err
	}

	hash := sha256.Sum256(jsonBytes)
	r.Hash = hex.EncodeToString(hash[:])
	return nil
}

type GetRecipeResponse struct {
	Result Recipe `json:"result"`
}

func (c *Client) GetRecipe(ctx context.Context, uid string) (*Recipe, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://paprikaapp.com/api/v2/sync/recipe/%s/", uid), nil)
	if err != nil {
		c.logger.Error("failed to create request", "error", err)
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		c.logger.Error("failed to get recipe", "error", err)
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.logger.Error("failed to get recipe", "status", resp.Status)
		return nil, fmt.Errorf("failed to get recipe: %s", resp.Status)
	}

	rawBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logger.Error("failed to read response body", "error", err)
		return nil, err
	}

	var recipeResp GetRecipeResponse
	if err := json.Unmarshal(rawBytes, &recipeResp); err != nil {
		c.logger.Error("failed to unmarshal response", "error", err)
		return nil, err
	}

	return &recipeResp.Result, nil
}

func (c *Client) DeleteRecipe(ctx context.Context, recipe Recipe) (*Recipe, error) {
	// Set the recipe to be in the trash
	// TODO: reverse-engineer full deletions; currently a user must go in-app to empty their trash and fully delete something
	recipe.InTrash = true
	return c.SaveRecipe(ctx, recipe)
}

// SaveRecipe saves a recipe to the Paprika API. If the recipe already exists, it will be updated.
// If the recipe does not exist, it will be created.
func (c *Client) SaveRecipe(ctx context.Context, recipe Recipe) (*Recipe, error) {
	// Categories must serialize as [] not null. A nil []string marshals to "null",
	// which breaks the Paprika .NET clients' sync deserializer with
	// "Value cannot be null. Parameter name: collection" (see issue #7).
	// Normalize here so every caller is protected, not just the create/update handlers.
	if recipe.Categories == nil {
		recipe.Categories = []string{}
	}
	recipe.updateCreated()
	recipe.generateUUID()
	if err := recipe.updateHash(); err != nil {
		return nil, err
	}

	if err := c.postV2(ctx, fmt.Sprintf("https://paprikaapp.com/api/v2/sync/recipe/%s/", recipe.UID), recipe); err != nil {
		return nil, fmt.Errorf("failed to save recipe: %w", err)
	}

	defer c.notify(ctx)
	return &recipe, nil
}

// notify sends a POST to /v2/sync/notify, which tells all Paprika clients to sync.
// We usually defer this call after a recipe is created/updated/deleted, since we don't care whether it suceeds or not.
func (c *Client) notify(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://paprikaapp.com/api/v2/sync/notify", nil)
	if err != nil {
		c.logger.Error("failed to create request", "error", err)
		return err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		c.logger.Error("failed to notify", "error", err)
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.logger.Error("failed to notify", "status", resp.Status)
		return fmt.Errorf("failed to notify: %s", resp.Status)
	}

	return nil
}

// isErrorResponse checks if the response body contains an error message
// and returns an error if it does. The Paprika API is very inconsistent with how it returns errors;
// sometimes a successful status code can be returned but an error is still returned in the body
func isErrorResponse(body []byte) error {
	var errResp errorResponse
	if err := json.Unmarshal(body, &errResp); err != nil {
		// Not even valid JSON
		return err
	}

	// Check if it's likely an error response
	if errResp.Error.Message != "" || errResp.Error.Code != 0 {
		return fmt.Errorf("error: %s (code: %d)", errResp.Error.Message, errResp.Error.Code)
	}

	return nil
}

// postV2 sends a gzipped JSON payload to a V2 API endpoint using multipart form encoding.
// Uses the Bearer token set on the HTTP client's roundTripper.
func (c *Client) postV2(ctx context.Context, endpoint string, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to marshal data: %w", err)
	}

	// Gzip the data
	var gzipBuf bytes.Buffer
	gzWriter := gzip.NewWriter(&gzipBuf)
	if _, err := gzWriter.Write(data); err != nil {
		gzWriter.Close()
		return fmt.Errorf("failed to gzip data: %w", err)
	}
	if err := gzWriter.Close(); err != nil {
		return fmt.Errorf("failed to close gzip writer: %w", err)
	}

	// Build multipart form
	var body bytes.Buffer
	mpWriter := multipart.NewWriter(&body)
	part, err := mpWriter.CreateFormFile("data", "data")
	if err != nil {
		return fmt.Errorf("failed to create form file: %w", err)
	}
	if _, err := part.Write(gzipBuf.Bytes()); err != nil {
		return fmt.Errorf("failed to write form data: %w", err)
	}
	if err := mpWriter.Close(); err != nil {
		return fmt.Errorf("failed to close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", mpWriter.FormDataContentType())
	req.ContentLength = int64(body.Len())

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	rawBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s: %s", resp.Status, string(rawBytes))
	}

	if err := isErrorResponse(rawBytes); err != nil {
		return err
	}

	return nil
}



type GroceryList struct {
	UID            string `json:"uid"`
	Name           string `json:"name"`
	OrderFlag      int    `json:"order_flag"`
	IsDefault      bool   `json:"is_default"`
	RemindersList  string `json:"reminders_list"`
	Deleted        bool   `json:"deleted"`
}

type GroceryListResponse struct {
	Result []GroceryList `json:"result"`
}

// ListGroceryLists retrieves all grocery lists from the Paprika API
func (c *Client) ListGroceryLists(ctx context.Context) (*GroceryListResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://paprikaapp.com/api/v2/sync/grocerylists", nil)
	if err != nil {
		c.logger.Error("failed to create request", "error", err)
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		c.logger.Error("failed to get grocery lists", "error", err)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.logger.Error("failed to get grocery lists", "status", resp.Status)
		return nil, fmt.Errorf("failed to get grocery lists: %s", resp.Status)
	}

	rawBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logger.Error("failed to read response body", "error", err)
		return nil, err
	}

	var groceryListResp GroceryListResponse
	if err := json.Unmarshal(rawBytes, &groceryListResp); err != nil {
		c.logger.Error("failed to unmarshal grocery lists response", "error", err)
		return nil, err
	}

	c.logger.Info("Retrieved grocery lists", "count", len(groceryListResp.Result))
	return &groceryListResp, nil
}

// ListMealPlan retrieves meal plan data from Paprika API
func (c *Client) ListMealPlan(ctx context.Context) (*MealPlanResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://paprikaapp.com/api/v2/sync/meals", nil)
	if err != nil {
		c.logger.Error("failed to create request", "error", err)
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		c.logger.Error("failed to get meals", "error", err)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.logger.Error("failed to get meals", "status", resp.Status)
		return nil, fmt.Errorf("failed to get meals: %s", resp.Status)
	}

	rawBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logger.Error("failed to read response body", "error", err)
		return nil, err
	}

	var mealPlanResp MealPlanResponse
	if err := json.Unmarshal(rawBytes, &mealPlanResp); err != nil {
		c.logger.Error("failed to unmarshal meal plan response", "error", err)
		return nil, err
	}

	c.logger.Info("Retrieved meal plan", "count", len(mealPlanResp.Result))
	return &mealPlanResp, nil
}

// ListGroceries retrieves grocery list data from Paprika API
func (c *Client) ListGroceries(ctx context.Context) (*GroceryResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://paprikaapp.com/api/v2/sync/groceries", nil)
	if err != nil {
		c.logger.Error("failed to create request", "error", err)
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		c.logger.Error("failed to get groceries", "error", err)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.logger.Error("failed to get groceries", "status", resp.Status)
		return nil, fmt.Errorf("failed to get groceries: %s", resp.Status)
	}

	rawBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logger.Error("failed to read response body", "error", err)
		return nil, err
	}

	var groceryResp GroceryResponse
	if err := json.Unmarshal(rawBytes, &groceryResp); err != nil {
		c.logger.Error("failed to unmarshal grocery response", "error", err)
		return nil, err
	}

	c.logger.Info("Retrieved groceries", "count", len(groceryResp.Result))
	return &groceryResp, nil
}

// SaveMealPlan saves a meal plan entry to the Paprika API.
// Meals use the bulk array endpoint POST /api/v2/sync/meals/ (not the per-item pattern recipes use).
func (c *Client) SaveMealPlan(ctx context.Context, meal MealPlan) (*MealPlan, error) {
	if meal.UID == "" {
		meal.UID = strings.ToUpper(uuid.New().String())
	}
	meal.Deleted = false

	c.logger.Info("Saving meal", "uid", meal.UID, "name", meal.Name, "date", meal.Date, "type", meal.Type)

	if err := c.postV2(ctx, "https://www.paprikaapp.com/api/v2/sync/meals/", []MealPlan{meal}); err != nil {
		return nil, fmt.Errorf("failed to save meal: %w", err)
	}

	defer c.notify(ctx)
	return &meal, nil
}

// DeleteMealPlan soft-deletes a meal plan entry by setting the deleted flag
func (c *Client) DeleteMealPlan(ctx context.Context, uid string) error {
	c.logger.Info("Soft-deleting meal", "uid", uid)

	meal := MealPlan{UID: uid, Deleted: true}
	if err := c.postV2(ctx, "https://www.paprikaapp.com/api/v2/sync/meals/", []MealPlan{meal}); err != nil {
		return fmt.Errorf("failed to delete meal: %w", err)
	}

	defer c.notify(ctx)
	return nil
}

// SaveGroceryItem saves a grocery item to the Paprika API.
// Groceries use the bulk array endpoint POST /api/v2/sync/groceries/ (not the per-item pattern recipes use).
func (c *Client) SaveGroceryItem(ctx context.Context, item GroceryItem) (*GroceryItem, error) {
	if item.UID == "" {
		item.UID = strings.ToUpper(uuid.New().String())
	}
	if item.Name == "" {
		item.Name = item.Ingredient
	}

	c.logger.Info("Saving grocery item", "uid", item.UID, "ingredient", item.Ingredient, "aisle", item.Aisle)

	if err := c.postV2(ctx, "https://www.paprikaapp.com/api/v2/sync/groceries/", []GroceryItem{item}); err != nil {
		return nil, fmt.Errorf("failed to save grocery item: %w", err)
	}

	defer c.notify(ctx)
	return &item, nil
}

// DeleteGroceryItem soft-deletes a grocery item by setting the deleted flag
func (c *Client) DeleteGroceryItem(ctx context.Context, uid string) error {
	c.logger.Info("Soft-deleting grocery item", "uid", uid)

	item := GroceryItem{UID: uid, Deleted: true}
	if err := c.postV2(ctx, "https://www.paprikaapp.com/api/v2/sync/groceries/", []GroceryItem{item}); err != nil {
		return fmt.Errorf("failed to delete grocery item: %w", err)
	}

	defer c.notify(ctx)
	return nil
}
