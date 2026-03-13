"""Sample Python app with known vulnerabilities for testing."""

import sqlite3

# Hardcoded secret (gitleaks should catch this)
API_KEY = "sk-ant-api03-FAKE_KEY_FOR_TESTING_ONLY_1234567890abcdef"
DATABASE_PASSWORD = "super_secret_password_123"


def get_user(user_id):
    """SQL injection vulnerability — user input directly in query string."""
    conn = sqlite3.connect("app.db")
    cursor = conn.cursor()
    # BAD: SQL injection via string formatting
    query = f"SELECT * FROM users WHERE id = '{user_id}'"
    cursor.execute(query)
    return cursor.fetchone()


def search_users(name):
    """Another SQL injection example."""
    conn = sqlite3.connect("app.db")
    cursor = conn.cursor()
    # BAD: SQL injection via string concatenation
    cursor.execute("SELECT * FROM users WHERE name LIKE '%" + name + "%'")
    return cursor.fetchall()


def process_data(data):
    """Unsafe eval usage."""
    # BAD: eval on user input
    result = eval(data)
    return result
