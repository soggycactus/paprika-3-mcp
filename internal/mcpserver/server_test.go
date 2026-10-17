package mcpserver

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/soggycactus/paprika-3-mcp/internal/paprika"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockClient implements PaprikaClient for unit testing.
type mockClient struct {
	recipes      []paprika.Recipe
	mealPlans    []paprika.MealPlan
	groceries    []paprika.GroceryItem
	groceryLists []paprika.GroceryList

	// Track calls for assertions
	deletedRecipeUIDs  []string
	deletedGroceryUIDs []string
	deletedMealUIDs    []string
	savedGroceryItems  []paprika.GroceryItem
}

func (m *mockClient) ListRecipes(ctx context.Context) (*paprika.RecipeList, error) {
	var result []struct {
		UID  string `json:"uid"`
		Hash string `json:"hash"`
	}
	for _, r := range m.recipes {
		result = append(result, struct {
			UID  string `json:"uid"`
			Hash string `json:"hash"`
		}{UID: r.UID, Hash: r.Hash})
	}
	return &paprika.RecipeList{Result: result}, nil
}

func (m *mockClient) GetRecipe(ctx context.Context, uid string) (*paprika.Recipe, error) {
	for _, r := range m.recipes {
		if r.UID == uid {
			return &r, nil
		}
	}
	return nil, fmt.Errorf("recipe not found: %s", uid)
}

func (m *mockClient) SaveRecipe(ctx context.Context, recipe paprika.Recipe) (*paprika.Recipe, error) {
	return &recipe, nil
}

func (m *mockClient) DeleteRecipe(ctx context.Context, recipe paprika.Recipe) (*paprika.Recipe, error) {
	m.deletedRecipeUIDs = append(m.deletedRecipeUIDs, recipe.UID)
	recipe.InTrash = true
	return &recipe, nil
}

func (m *mockClient) ListMealPlan(ctx context.Context) (*paprika.MealPlanResponse, error) {
	return &paprika.MealPlanResponse{Result: m.mealPlans}, nil
}

func (m *mockClient) SaveMealPlan(ctx context.Context, meal paprika.MealPlan) (*paprika.MealPlan, error) {
	meal.UID = "MOCK-MEAL-UID"
	return &meal, nil
}

func (m *mockClient) DeleteMealPlan(ctx context.Context, uid string) error {
	m.deletedMealUIDs = append(m.deletedMealUIDs, uid)
	return nil
}

func (m *mockClient) ListGroceries(ctx context.Context) (*paprika.GroceryResponse, error) {
	return &paprika.GroceryResponse{Result: m.groceries}, nil
}

func (m *mockClient) ListGroceryLists(ctx context.Context) (*paprika.GroceryListResponse, error) {
	return &paprika.GroceryListResponse{Result: m.groceryLists}, nil
}

func (m *mockClient) SaveGroceryItem(ctx context.Context, item paprika.GroceryItem) (*paprika.GroceryItem, error) {
	item.UID = "MOCK-GROCERY-UID"
	m.savedGroceryItems = append(m.savedGroceryItems, item)
	return &item, nil
}

func (m *mockClient) DeleteGroceryItem(ctx context.Context, uid string) error {
	m.deletedGroceryUIDs = append(m.deletedGroceryUIDs, uid)
	return nil
}

func newTestServer(mock *mockClient) *Server {
	return &Server{
		paprika3: mock,
		logger:   slog.Default(),
	}
}

func callToolRequest(args map[string]interface{}) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments,omitempty"`
			Meta      *struct {
				ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
			} `json:"_meta,omitempty"`
		}{
			Arguments: args,
		},
	}
}

func TestMealTypeName(t *testing.T) {
	assert.Equal(t, "Breakfast", paprika.MealTypeName(paprika.MealTypeBreakfast))
	assert.Equal(t, "Lunch", paprika.MealTypeName(paprika.MealTypeLunch))
	assert.Equal(t, "Dinner", paprika.MealTypeName(paprika.MealTypeDinner))
	assert.Equal(t, "Meal", paprika.MealTypeName(99))
}

func TestListMealPlan_DateFiltering(t *testing.T) {
	mock := &mockClient{
		mealPlans: []paprika.MealPlan{
			{UID: "1", Name: "Monday Dinner", Date: "2025-03-10 00:00:00", Type: paprika.MealTypeDinner},
			{UID: "2", Name: "Tuesday Lunch", Date: "2025-03-11 00:00:00", Type: paprika.MealTypeLunch},
			{UID: "3", Name: "Wednesday Breakfast", Date: "2025-03-12 00:00:00", Type: paprika.MealTypeBreakfast},
			{UID: "4", Name: "Deleted Meal", Date: "2025-03-11 00:00:00", Type: paprika.MealTypeDinner, Deleted: true},
		},
	}
	s := newTestServer(mock)
	ctx := context.Background()

	t.Run("no filters returns all non-deleted", func(t *testing.T) {
		result, err := s.listMealPlan(ctx, callToolRequest(map[string]interface{}{}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "Monday Dinner")
		assert.Contains(t, text, "Tuesday Lunch")
		assert.Contains(t, text, "Wednesday Breakfast")
		assert.NotContains(t, text, "Deleted Meal")
	})

	t.Run("start_date filters correctly", func(t *testing.T) {
		result, err := s.listMealPlan(ctx, callToolRequest(map[string]interface{}{
			"start_date": "2025-03-11",
		}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.NotContains(t, text, "Monday Dinner")
		assert.Contains(t, text, "Tuesday Lunch")
		assert.Contains(t, text, "Wednesday Breakfast")
	})

	t.Run("end_date filters correctly", func(t *testing.T) {
		result, err := s.listMealPlan(ctx, callToolRequest(map[string]interface{}{
			"end_date": "2025-03-11",
		}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "Monday Dinner")
		assert.Contains(t, text, "Tuesday Lunch")
		assert.NotContains(t, text, "Wednesday Breakfast")
	})

	t.Run("exact date match includes the meal", func(t *testing.T) {
		result, err := s.listMealPlan(ctx, callToolRequest(map[string]interface{}{
			"start_date": "2025-03-10",
			"end_date":   "2025-03-10",
		}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "Monday Dinner")
		assert.NotContains(t, text, "Tuesday Lunch")
	})

	t.Run("deleted meals are excluded", func(t *testing.T) {
		result, err := s.listMealPlan(ctx, callToolRequest(map[string]interface{}{
			"start_date": "2025-03-11",
			"end_date":   "2025-03-11",
		}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "Tuesday Lunch")
		assert.NotContains(t, text, "Deleted Meal")
	})
}

func TestListGroceries_PurchaseFilter(t *testing.T) {
	mock := &mockClient{
		groceries: []paprika.GroceryItem{
			{UID: "1", Ingredient: "Milk", Purchased: false, Aisle: "Dairy", ListUID: "L1"},
			{UID: "2", Ingredient: "Eggs", Purchased: true, Aisle: "Dairy", ListUID: "L1"},
			{UID: "3", Ingredient: "Bread", Purchased: false, Aisle: "Bakery", ListUID: "L1"},
			{UID: "4", Ingredient: "Deleted Item", Purchased: false, Deleted: true, ListUID: "L1"},
		},
		groceryLists: []paprika.GroceryList{{UID: "L1", Name: "My List", IsDefault: true}},
	}
	s := newTestServer(mock)
	ctx := context.Background()

	t.Run("all returns non-deleted items", func(t *testing.T) {
		result, err := s.listGroceries(ctx, callToolRequest(map[string]interface{}{
			"filter": "all",
		}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "Milk")
		assert.Contains(t, text, "Eggs")
		assert.Contains(t, text, "Bread")
		assert.NotContains(t, text, "Deleted Item")
	})

	t.Run("purchased filter", func(t *testing.T) {
		result, err := s.listGroceries(ctx, callToolRequest(map[string]interface{}{
			"filter": "purchased",
		}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.NotContains(t, text, "Milk")
		assert.Contains(t, text, "Eggs")
		assert.NotContains(t, text, "Bread")
	})

	t.Run("unpurchased filter", func(t *testing.T) {
		result, err := s.listGroceries(ctx, callToolRequest(map[string]interface{}{
			"filter": "unpurchased",
		}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "Milk")
		assert.NotContains(t, text, "Eggs")
		assert.Contains(t, text, "Bread")
	})

	t.Run("default filter is all", func(t *testing.T) {
		result, err := s.listGroceries(ctx, callToolRequest(map[string]interface{}{}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "3 grocery item(s)")
	})

	t.Run("items grouped by aisle and aisles sorted", func(t *testing.T) {
		result, err := s.listGroceries(ctx, callToolRequest(map[string]interface{}{}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		// Aisles should appear in alphabetical order: Bakery before Dairy
		bakeryIdx := strings.Index(text, "## Bakery")
		dairyIdx := strings.Index(text, "## Dairy")
		require.NotEqual(t, -1, bakeryIdx, "should have Bakery aisle")
		require.NotEqual(t, -1, dairyIdx, "should have Dairy aisle")
		assert.Less(t, bakeryIdx, dairyIdx, "Bakery should appear before Dairy")
	})

	t.Run("items without aisle grouped under Other", func(t *testing.T) {
		mockWithNoAisle := &mockClient{
			groceries: []paprika.GroceryItem{
				{UID: "1", Ingredient: "Mystery Item", Aisle: "", ListUID: "L1"},
			},
			groceryLists: []paprika.GroceryList{{UID: "L1", Name: "My List", IsDefault: true}},
		}
		s2 := newTestServer(mockWithNoAisle)
		result, err := s2.listGroceries(ctx, callToolRequest(map[string]interface{}{}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "## Other")
		assert.Contains(t, text, "Mystery Item")
	})
}

func TestListRecipes(t *testing.T) {
	mock := &mockClient{
		recipes: []paprika.Recipe{
			{UID: "1", Name: "Chicken Tikka Masala", Ingredients: "chicken, yogurt, spices", Description: "Indian curry"},
			{UID: "2", Name: "Spaghetti Bolognese", Ingredients: "pasta, beef, tomatoes", Description: "Italian classic"},
			{UID: "3", Name: "Chicken Caesar Salad", Ingredients: "chicken, romaine, parmesan", Description: "Light lunch"},
			{UID: "4", Name: "Trashed Recipe", Ingredients: "nothing", Description: "Gone", InTrash: true},
		},
	}
	s := newTestServer(mock)
	ctx := context.Background()

	t.Run("returns all non-trashed sorted by name", func(t *testing.T) {
		result, err := s.listRecipes(ctx, callToolRequest(map[string]interface{}{}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "Chicken Caesar Salad")
		assert.Contains(t, text, "Chicken Tikka Masala")
		assert.Contains(t, text, "Spaghetti Bolognese")
		assert.NotContains(t, text, "Trashed Recipe")
		// Verify alphabetical order
		caesarIdx := strings.Index(text, "Chicken Caesar Salad")
		tikkaIdx := strings.Index(text, "Chicken Tikka Masala")
		spaghettiIdx := strings.Index(text, "Spaghetti Bolognese")
		assert.Less(t, caesarIdx, tikkaIdx)
		assert.Less(t, tikkaIdx, spaghettiIdx)
	})

	t.Run("limit caps results", func(t *testing.T) {
		result, err := s.listRecipes(ctx, callToolRequest(map[string]interface{}{
			"limit": float64(2),
		}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "Found 2 recipe(s)")
	})
}

func TestSearchRecipes(t *testing.T) {
	mock := &mockClient{
		recipes: []paprika.Recipe{
			{UID: "1", Name: "Chicken Tikka Masala", Ingredients: "chicken, yogurt, spices", Description: "Indian curry"},
			{UID: "2", Name: "Spaghetti Bolognese", Ingredients: "pasta, beef, tomatoes", Description: "Italian classic"},
			{UID: "3", Name: "Chicken Caesar Salad", Ingredients: "chicken, romaine, parmesan", Description: "Light lunch"},
			{UID: "4", Name: "Trashed Recipe", Ingredients: "nothing", Description: "Gone", InTrash: true},
		},
	}
	s := newTestServer(mock)
	ctx := context.Background()

	t.Run("empty query returns error", func(t *testing.T) {
		_, err := s.searchRecipes(ctx, callToolRequest(map[string]interface{}{}))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "query is required")
	})

	t.Run("keyword search matches name", func(t *testing.T) {
		result, err := s.searchRecipes(ctx, callToolRequest(map[string]interface{}{
			"query": "spaghetti",
		}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "Spaghetti Bolognese")
		assert.NotContains(t, text, "Chicken")
	})

	t.Run("keyword search matches ingredients", func(t *testing.T) {
		result, err := s.searchRecipes(ctx, callToolRequest(map[string]interface{}{
			"query": "romaine",
		}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "Chicken Caesar Salad")
		assert.NotContains(t, text, "Tikka")
	})

	t.Run("search is case insensitive", func(t *testing.T) {
		result, err := s.searchRecipes(ctx, callToolRequest(map[string]interface{}{
			"query": "CHICKEN",
		}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "Chicken Tikka Masala")
		assert.Contains(t, text, "Chicken Caesar Salad")
	})

	t.Run("limit caps results", func(t *testing.T) {
		result, err := s.searchRecipes(ctx, callToolRequest(map[string]interface{}{
			"query": "chicken",
			"limit": float64(1),
		}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "Found 1 recipe(s)")
	})

	t.Run("multi-word search matches all words", func(t *testing.T) {
		result, err := s.searchRecipes(ctx, callToolRequest(map[string]interface{}{
			"query": "chicken yogurt",
		}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "Chicken Tikka Masala")
		assert.NotContains(t, text, "Caesar Salad")
	})

	t.Run("multi-word search requires all words", func(t *testing.T) {
		result, err := s.searchRecipes(ctx, callToolRequest(map[string]interface{}{
			"query": "chicken tomatoes",
		}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "No recipes found matching your search")
	})

	t.Run("no match returns message", func(t *testing.T) {
		result, err := s.searchRecipes(ctx, callToolRequest(map[string]interface{}{
			"query": "nonexistent",
		}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "No recipes found matching your search")
	})
}

func TestRemoveGroceryItem(t *testing.T) {
	mock := &mockClient{
		groceries: []paprika.GroceryItem{
			{UID: "1", Ingredient: "Whole Milk", Name: "Whole Milk"},
			{UID: "2", Ingredient: "Almond Milk", Name: "Almond Milk"},
			{UID: "3", Ingredient: "Eggs", Name: "Eggs"},
		},
	}
	s := newTestServer(mock)
	ctx := context.Background()

	t.Run("exact match removes item", func(t *testing.T) {
		mock.deletedGroceryUIDs = nil
		result, err := s.removeGroceryItem(ctx, callToolRequest(map[string]interface{}{
			"item_name": "Eggs",
		}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "Removed **Eggs**")
		assert.Equal(t, []string{"3"}, mock.deletedGroceryUIDs)
	})

	t.Run("ambiguous name is refused and lists uids", func(t *testing.T) {
		mock.deletedGroceryUIDs = nil
		result, err := s.removeGroceryItem(ctx, callToolRequest(map[string]interface{}{
			"item_name": "milk",
		}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "Multiple Items Match")
		assert.Contains(t, text, "uid: 1")
		assert.Contains(t, text, "uid: 2")
		assert.Empty(t, mock.deletedGroceryUIDs)
	})

	t.Run("uid removes that exact item", func(t *testing.T) {
		mock.deletedGroceryUIDs = nil
		result, err := s.removeGroceryItem(ctx, callToolRequest(map[string]interface{}{
			"uid": "2",
		}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "Removed **Almond Milk**")
		assert.Equal(t, []string{"2"}, mock.deletedGroceryUIDs)
	})

	t.Run("requires uid or name", func(t *testing.T) {
		_, err := s.removeGroceryItem(ctx, callToolRequest(map[string]interface{}{}))
		assert.Error(t, err)
	})

	t.Run("no match returns message", func(t *testing.T) {
		mock.deletedGroceryUIDs = nil
		result, err := s.removeGroceryItem(ctx, callToolRequest(map[string]interface{}{
			"item_name": "butter",
		}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "No Items Found")
		assert.Empty(t, mock.deletedGroceryUIDs)
	})

	t.Run("case insensitive", func(t *testing.T) {
		mock.deletedGroceryUIDs = nil
		result, err := s.removeGroceryItem(ctx, callToolRequest(map[string]interface{}{
			"item_name": "EGGS",
		}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "Removed **Eggs**")
		assert.Equal(t, []string{"3"}, mock.deletedGroceryUIDs)
	})
}

func TestListGroceryLists_FiltersDeleted(t *testing.T) {
	mock := &mockClient{
		groceryLists: []paprika.GroceryList{
			{UID: "1", Name: "My List", IsDefault: true},
			{UID: "2", Name: "Trader Joe's", IsDefault: false},
			{UID: "3", Name: "Old List", IsDefault: false, Deleted: true},
		},
	}
	s := newTestServer(mock)
	ctx := context.Background()

	result, err := s.listGroceryLists(ctx, callToolRequest(map[string]interface{}{}))
	require.NoError(t, err)
	text := result.Content[0].(mcp.TextContent).Text
	assert.Contains(t, text, "My List")
	assert.Contains(t, text, "(default)")
	assert.Contains(t, text, "Trader Joe's")
	assert.NotContains(t, text, "Old List")
}

func TestDeleteRecipe(t *testing.T) {
	mock := &mockClient{
		recipes: []paprika.Recipe{
			{UID: "recipe-1", Name: "Test Recipe", Ingredients: "flour", Directions: "mix"},
			{UID: "recipe-2", Name: "Other Recipe", Ingredients: "sugar", Directions: "bake"},
		},
	}
	s := newTestServer(mock)
	ctx := context.Background()

	t.Run("deletes recipe by UID", func(t *testing.T) {
		result, err := s.deleteRecipe(ctx, callToolRequest(map[string]interface{}{
			"uid": "recipe-1",
		}))
		require.NoError(t, err)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "Test Recipe")
		assert.Contains(t, text, "trash")
		assert.Equal(t, []string{"recipe-1"}, mock.deletedRecipeUIDs)
	})

	t.Run("returns error for missing UID", func(t *testing.T) {
		_, err := s.deleteRecipe(ctx, callToolRequest(map[string]interface{}{}))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "uid is required")
	})

	t.Run("returns error for unknown UID", func(t *testing.T) {
		_, err := s.deleteRecipe(ctx, callToolRequest(map[string]interface{}{
			"uid": "nonexistent",
		}))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get recipe")
	})
}

func TestListGroceries_ListSelection(t *testing.T) {
	mock := &mockClient{
		groceries: []paprika.GroceryItem{
			{UID: "a", Ingredient: "Butter", ListUID: "L1"},
			{UID: "b", Ingredient: "Paper Towels", ListUID: "L2"},
			{UID: "c", Ingredient: "Crusty bread", ListUID: ""},
			{UID: "d", Ingredient: "Canned peaches", ListUID: "GONE"},
		},
		groceryLists: []paprika.GroceryList{
			{UID: "L1", Name: "My Grocery List", IsDefault: true},
			{UID: "L2", Name: "Costco"},
			{UID: "GONE", Name: "Old", Deleted: true},
		},
	}
	s := newTestServer(mock)
	ctx := context.Background()
	text := func(args map[string]interface{}) string {
		result, err := s.listGroceries(ctx, callToolRequest(args))
		require.NoError(t, err)
		return result.Content[0].(mcp.TextContent).Text
	}

	t.Run("defaults to the default list and hides orphans", func(t *testing.T) {
		out := text(map[string]interface{}{})
		assert.Contains(t, out, "# Grocery List: My Grocery List")
		assert.Contains(t, out, "**Butter** — list: My Grocery List · uid: a")
		assert.NotContains(t, out, "Paper Towels")
		assert.NotContains(t, out, "Crusty bread")
		assert.NotContains(t, out, "Canned peaches")
	})

	t.Run("selects a list by name", func(t *testing.T) {
		out := text(map[string]interface{}{"list": "costco"})
		assert.Contains(t, out, "Paper Towels")
		assert.NotContains(t, out, "Butter")
	})

	t.Run("include_orphans labels items on no live list", func(t *testing.T) {
		out := text(map[string]interface{}{"include_orphans": true})
		assert.Contains(t, out, "Butter")
		assert.Contains(t, out, "**Crusty bread** — list: (none — orphaned) · uid: c")
		assert.Contains(t, out, "**Canned peaches** — list: (none — orphaned) · uid: d")
		assert.NotContains(t, out, "Paper Towels")
	})

	t.Run("all shows every list and orphans", func(t *testing.T) {
		out := text(map[string]interface{}{"list": "all"})
		for _, name := range []string{"Butter", "Paper Towels", "Crusty bread", "Canned peaches"} {
			assert.Contains(t, out, name)
		}
	})

	t.Run("unknown list is an error", func(t *testing.T) {
		_, err := s.listGroceries(ctx, callToolRequest(map[string]interface{}{"list": "Trader Joe's"}))
		assert.Error(t, err)
	})
}

func TestSetGroceryPurchased(t *testing.T) {
	mock := &mockClient{
		groceries: []paprika.GroceryItem{
			{UID: "1", Ingredient: "Butter", Name: "Butter", ListUID: "L1", Aisle: "Dairy", Quantity: "1 lb"},
			{UID: "2", Ingredient: "Butter", Name: "Butter", ListUID: "L1"},
		},
		groceryLists: []paprika.GroceryList{{UID: "L1", Name: "My Grocery List", IsDefault: true}},
	}
	s := newTestServer(mock)
	ctx := context.Background()

	t.Run("by uid saves the full item as purchased", func(t *testing.T) {
		mock.savedGroceryItems = nil
		result, err := s.setGroceryPurchased(ctx, callToolRequest(map[string]interface{}{"uid": "1"}))
		require.NoError(t, err)
		assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "as purchased")
		require.Len(t, mock.savedGroceryItems, 1)
		saved := mock.savedGroceryItems[0]
		assert.True(t, saved.Purchased)
		assert.Equal(t, "L1", saved.ListUID)
		assert.Equal(t, "Dairy", saved.Aisle)
		assert.Equal(t, "1 lb", saved.Quantity)
	})

	t.Run("purchased false unchecks", func(t *testing.T) {
		mock.savedGroceryItems = nil
		_, err := s.setGroceryPurchased(ctx, callToolRequest(map[string]interface{}{"uid": "1", "purchased": false}))
		require.NoError(t, err)
		require.Len(t, mock.savedGroceryItems, 1)
		assert.False(t, mock.savedGroceryItems[0].Purchased)
	})

	t.Run("duplicate name is refused", func(t *testing.T) {
		mock.savedGroceryItems = nil
		result, err := s.setGroceryPurchased(ctx, callToolRequest(map[string]interface{}{"item_name": "butter"}))
		require.NoError(t, err)
		assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "Multiple Items Match")
		assert.Empty(t, mock.savedGroceryItems)
	})
}
