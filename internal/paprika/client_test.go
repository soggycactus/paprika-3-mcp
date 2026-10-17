package paprika_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/soggycactus/paprika-3-mcp/internal/paprika"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(t *testing.T) *paprika.Client {
	t.Helper()
	username := os.Getenv("PAPRIKA_USERNAME")
	password := os.Getenv("PAPRIKA_PASSWORD")
	if username == "" || password == "" {
		t.Skip("PAPRIKA_USERNAME and PAPRIKA_PASSWORD must be set")
	}
	client, err := paprika.NewClient(username, password, "dev", nil)
	require.NoError(t, err)
	return client
}

func TestClient(t *testing.T) {
	client := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	testRecipe := paprika.Recipe{
		Name:        fmt.Sprintf("Test Recipe - %d", time.Now().Unix()),
		Notes:       "Notes",
		Directions:  "Directions",
		Ingredients: "Ingredients",
		Servings:    "Servings",
		Source:      "Source",
		SourceURL:   "URL",
		Categories:  []string{},
	}
	recipe, err := client.SaveRecipe(ctx, testRecipe)
	require.NoError(t, err)

	recipe, err = client.GetRecipe(ctx, recipe.UID)
	require.NoError(t, err)
	assert.NotEmpty(t, recipe.UID)
	assert.Equal(t, testRecipe.Name, recipe.Name)
	assert.Equal(t, testRecipe.Notes, recipe.Notes)
	assert.Equal(t, testRecipe.Directions, recipe.Directions)
	assert.Equal(t, testRecipe.Ingredients, recipe.Ingredients)
	assert.Equal(t, testRecipe.Servings, recipe.Servings)
	assert.Equal(t, testRecipe.Source, recipe.Source)
	assert.Equal(t, testRecipe.SourceURL, recipe.SourceURL)
	assert.Equal(t, testRecipe.Categories, recipe.Categories)

	t.Logf("Created and fetched recipe: %+v", recipe)

	newDescription := "Updated Description"
	recipe.Description = newDescription
	uid := recipe.UID
	recipe, err = client.SaveRecipe(ctx, *recipe)
	require.NoError(t, err)
	assert.Equal(t, newDescription, recipe.Description)
	assert.Equal(t, uid, recipe.UID)
	assert.Equal(t, testRecipe.Name, recipe.Name)
	assert.Equal(t, testRecipe.Notes, recipe.Notes)
	assert.Equal(t, testRecipe.Directions, recipe.Directions)
	assert.Equal(t, testRecipe.Ingredients, recipe.Ingredients)
	assert.Equal(t, testRecipe.Servings, recipe.Servings)
	assert.Equal(t, testRecipe.Source, recipe.Source)
	assert.Equal(t, testRecipe.SourceURL, recipe.SourceURL)
	assert.Equal(t, testRecipe.Categories, recipe.Categories)

	t.Logf("Updated recipe: %+v", recipe)

	_, err = client.DeleteRecipe(ctx, *recipe)
	require.NoError(t, err)
	t.Logf("Deleted recipe: %s", recipe.Name)

	// Verify the test recipe appears in the recipe list
	recipes, err := client.ListRecipes(ctx)
	require.NoError(t, err)

	found := false
	for _, r := range recipes.Result {
		if r.UID == uid {
			found = true
			break
		}
	}
	assert.True(t, found, "test recipe should appear in recipe list (even after soft delete)")

	// Verify the trashed recipe is marked as in_trash
	trashedRecipe, err := client.GetRecipe(ctx, uid)
	require.NoError(t, err)
	assert.True(t, trashedRecipe.InTrash, "deleted recipe should be in trash")
}

func TestListGroceryLists(t *testing.T) {
	client := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := client.ListGroceryLists(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.Result, "account should have at least one grocery list")

	// Every account should have a default list
	hasDefault := false
	for _, list := range resp.Result {
		t.Logf("  List: %q  UID: %s  Default: %v", list.Name, list.UID, list.IsDefault)
		assert.NotEmpty(t, list.UID)
		assert.NotEmpty(t, list.Name)
		if list.IsDefault {
			hasDefault = true
		}
	}
	assert.True(t, hasDefault, "should have a default grocery list")
}

func TestMealPlan(t *testing.T) {
	client := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a meal plan entry
	testMeal := paprika.MealPlan{
		Date:      "2099-01-01 00:00:00",
		Name:      fmt.Sprintf("Test Meal - %d", time.Now().Unix()),
		Type:      paprika.MealTypeDinner,
		OrderFlag: 0,
	}
	savedMeal, err := client.SaveMealPlan(ctx, testMeal)
	require.NoError(t, err)
	assert.NotEmpty(t, savedMeal.UID)
	assert.Equal(t, testMeal.Name, savedMeal.Name)
	assert.Equal(t, testMeal.Date, savedMeal.Date)
	assert.Equal(t, testMeal.Type, savedMeal.Type)
	assert.False(t, savedMeal.Deleted)
	t.Logf("Created meal: %+v", savedMeal)

	// List meal plan and verify our entry is present
	mealPlanResp, err := client.ListMealPlan(ctx)
	require.NoError(t, err)
	assert.NotNil(t, mealPlanResp)

	found := false
	for _, meal := range mealPlanResp.Result {
		if meal.UID == savedMeal.UID {
			found = true
			assert.Equal(t, testMeal.Name, meal.Name)
			break
		}
	}
	assert.True(t, found, "saved meal should appear in meal plan list")

	// Delete the meal plan entry
	err = client.DeleteMealPlan(ctx, savedMeal.UID)
	require.NoError(t, err)
	t.Logf("Deleted meal: %s", savedMeal.UID)
}

func TestGroceries(t *testing.T) {
	client := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Add a grocery item (default list)
	testItem := paprika.GroceryItem{
		Ingredient: fmt.Sprintf("Test Ingredient - %d", time.Now().Unix()),
		Aisle:      "Produce",
		Quantity:   "2 lbs",
		Recipe:     "Test Recipe",
	}
	savedItem, err := client.SaveGroceryItem(ctx, testItem)
	require.NoError(t, err)
	assert.NotEmpty(t, savedItem.UID)
	assert.Equal(t, testItem.Ingredient, savedItem.Ingredient)
	assert.Equal(t, testItem.Aisle, savedItem.Aisle)
	assert.Equal(t, testItem.Quantity, savedItem.Quantity)
	assert.Equal(t, testItem.Ingredient, savedItem.Name, "Name should default to Ingredient")
	t.Logf("Created grocery item (default list): %+v", savedItem)

	// List groceries and verify our item is present
	groceryResp, err := client.ListGroceries(ctx)
	require.NoError(t, err)
	require.NotNil(t, groceryResp)

	found := false
	for _, item := range groceryResp.Result {
		if item.UID == savedItem.UID {
			found = true
			assert.Equal(t, testItem.Ingredient, item.Ingredient)
			t.Logf("Item added without ListUID has ListUID=%q from API", item.ListUID)
			break
		}
	}
	assert.True(t, found, "saved grocery item should appear in grocery list")

	// Clean up default list item
	err = client.DeleteGroceryItem(ctx, savedItem.UID)
	require.NoError(t, err)
	t.Logf("Deleted grocery item: %s", savedItem.UID)
}

func TestGroceryItemOnSpecificList(t *testing.T) {
	client := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// First, get available grocery lists
	listsResp, err := client.ListGroceryLists(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, listsResp.Result, "need at least one grocery list")

	// Pick a non-default list if available, otherwise use the first one
	targetList := listsResp.Result[0]
	for _, list := range listsResp.Result {
		if !list.IsDefault && !list.Deleted {
			targetList = list
			break
		}
	}
	t.Logf("Adding item to list: %q (UID: %s)", targetList.Name, targetList.UID)

	// Add a grocery item to the specific list
	testItem := paprika.GroceryItem{
		Ingredient: fmt.Sprintf("Test ListItem - %d", time.Now().Unix()),
		Aisle:      "Dairy",
		Quantity:   "1 gallon",
		ListUID:    targetList.UID,
	}
	savedItem, err := client.SaveGroceryItem(ctx, testItem)
	require.NoError(t, err)
	assert.NotEmpty(t, savedItem.UID)
	assert.Equal(t, targetList.UID, savedItem.ListUID, "item should be on the target list")
	t.Logf("Created grocery item on list %q: %+v", targetList.Name, savedItem)

	// List all groceries and verify our item has the correct list_uid
	groceryResp, err := client.ListGroceries(ctx)
	require.NoError(t, err)

	found := false
	for _, item := range groceryResp.Result {
		if item.UID == savedItem.UID {
			found = true
			assert.Equal(t, testItem.Ingredient, item.Ingredient)
			assert.Equal(t, targetList.UID, item.ListUID, "item should belong to target list")
			t.Logf("Verified item on list: ListUID=%s", item.ListUID)
			break
		}
	}
	assert.True(t, found, "saved grocery item should appear in grocery list")

	// Clean up
	err = client.DeleteGroceryItem(ctx, savedItem.UID)
	require.NoError(t, err)
	t.Logf("Deleted grocery item: %s", savedItem.UID)
}
