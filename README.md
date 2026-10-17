# paprika-3-mcp

A [Model Context Protocol (MCP)](https://modelcontextprotocol.io/introduction) server that connects your **Paprika 3** account to LLMs like Claude — manage recipes, plan meals, and build grocery lists through natural conversation.

### 🖼️ Example: Claude using the Paprika MCP server

<p align="center">
  <img src="docs/example.png" alt="MCP server running with Claude" />
</p>

## 🚀 Features

See anything missing? Open an issue on this repo to request a feature!

#### 📄 **Resources**

- Recipes ✅
- Recipe Photos 🚧

#### 🛠 **Tools**

**Recipe Management**
- `create_paprika_recipe` — Create new recipes in your Paprika app
- `update_paprika_recipe` — Modify existing recipes by UID
- `get_recipe` — Get full recipe details (ingredients, directions, etc.) by UID
- `list_recipes` — List all recipes with names, UIDs, and basic info (fast, parallel fetch)
- `search_recipes` — Deep search recipes by keyword across name, ingredients, and description
- `delete_recipe` — Move a recipe to the trash by UID (soft delete; recoverable in the Paprika app)

**Meal Planning**
- `list_meal_plan` — View scheduled meals, optionally filtered by date range
- `add_meal_to_plan` — Schedule a meal (Breakfast, Lunch, or Dinner) with an optional recipe link
- `remove_meal_from_plan` — Remove a meal from the plan by UID

**Grocery Management**
- `list_grocery_lists` — View all grocery lists (Paprika supports multiple lists, e.g. one per store)
- `list_groceries` — View grocery items across all lists, grouped by aisle, with purchase status
- `add_grocery_item` — Add an item to a grocery list (defaults to the default list; specify `list_uid` for others)
- `remove_grocery_item` — Remove an item by name (case-insensitive partial match)

## ⚙️ Prerequisites

- ✅ A Mac, Linux, or Windows system
- ✅ [Paprika 3](https://www.paprikaapp.com/) installed with cloud sync enabled
- ✅ Your Paprika 3 **username and password**
- ✅ Claude or any LLM client with **MCP tool support** enabled

## 🛠 Installation

You can download a prebuilt binary from the [Releases](https://github.com/soggycactus/paprika-3-mcp/releases) page.

### 🍎 macOS (via Homebrew)

If you're on macOS, the easiest way to install is with [Homebrew](https://brew.sh/):

```bash
brew tap soggycactus/tap
brew install paprika-3-mcp
```

### 🐧 Linux / 🪟 Windows

1. Go to the [latest release](https://github.com/soggycactus/paprika-3-mcp/releases).
2. Download the appropriate archive for your operating system and architecture:
   - `paprika-3-mcp_<version>_linux_amd64.zip` for Linux
   - `paprika-3-mcp_<version>_windows_amd64.zip` for Windows
3. Extract the zip archive:
   - **Linux**:
     ```bash
     unzip paprika-3-mcp_<version>_<os>_<arch>.zip
     ```
   - **Windows**:
     - Right-click the `.zip` file and select **Extract All**, or use a tool like 7-Zip.
4. Move the binary to a directory in your system's `$PATH`:

   - Linux:

     ```bash
     sudo mv paprika-3-mcp /usr/local/bin/
     ```

   - Windows:
     - Move `paprika-3-mcp.exe` to any folder in your `PATH` (e.g., `%USERPROFILE%\bin`)

### ✅ Test the installation

You can verify the server is installed by checking:

```bash
paprika-3-mcp --version
```

You should see:

```bash
paprika-3-mcp version v0.1.0
```

## 🤖 Setting up Claude

If you haven't setup MCP before, [first read more about how to install Claude Desktop client & configure an MCP server.](https://modelcontextprotocol.io/quickstart/user)

To add `paprika-3-mcp` to Claude, all you need to do is create another entry in the `mcpServers` section of your `claude_desktop_config.json` file:

```json
{
  "mcpServers": {
    "paprika-3": {
      "command": "paprika-3-mcp",
      "args": [
        "--username",
        "<your paprika 3 username (usually email)>",
        "--password",
        "<your paprika 3 password>"
      ]
    }
  }
}
```

Restart Claude and you should see the MCP server tools after clicking on the hammerhead icon:

![MCP server running with Claude](docs/install.png)

## 📄 License

This project is open source under the [MIT License](./LICENSE) © 2025 [Lucas Stephens](https://github.com/soggycactus).

---

#### 🗂 Miscellaneous

##### 📄 Where can I see the server logs?

The MCP server writes structured logs using Go’s `slog` with rotation via `lumberjack`. Log files are automatically created based on your operating system:

| Operating System | Log File Path                             |
| ---------------- | ----------------------------------------- |
| macOS            | `~/Library/Logs/paprika-3-mcp/server.log` |
| Linux            | `/var/log/paprika-3-mcp/server.log`       |
| Windows          | `%APPDATA%\paprika-3-mcp\server.log`      |
| Other / Unknown  | `/tmp/paprika-3-mcp/server.log`           |

> 💡 Logs are rotated automatically at 100MB, with only 5 backup files kept. Logs are also wiped after 10 days.
