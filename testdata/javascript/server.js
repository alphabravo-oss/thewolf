/**
 * Sample Express server with known vulnerabilities for testing.
 */

const express = require("express");
const app = express();

// XSS vulnerability — reflected user input
app.get("/search", (req, res) => {
  const query = req.query.q;
  // BAD: Reflected XSS — user input rendered directly in HTML
  res.send(`<h1>Results for: ${query}</h1>`);
});

// XSS vulnerability — stored
app.post("/comment", (req, res) => {
  const comment = req.body.comment;
  // BAD: Stored XSS — unsanitized user content
  res.send(`<div class="comment">${comment}</div>`);
});

// Path traversal
app.get("/file", (req, res) => {
  const filename = req.query.name;
  // BAD: Path traversal — user controls file path
  res.sendFile("/uploads/" + filename);
});

app.listen(3000, () => {
  console.log("Server running on port 3000");
});
