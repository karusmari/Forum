package handlers

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var location *time.Location

// this will initialize the location for a timezone as soon as the package is loaded
func init() {
	var err error
	location, err = time.LoadLocation("Europe/Helsinki") // UTC+2
	if err != nil {
		log.Printf("Error loading timezone: %v", err)
		location = time.UTC
	}
}

func (h *Handler) AddComment(w http.ResponseWriter, r *http.Request) {
	// Check method
	if r.Method != http.MethodPost {
		log.Printf("Invalid method: %s", r.Method)
		h.ErrorHandler(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check authentication
	user := h.GetSessionUser(r)
	if user == nil {
		log.Printf("User not authenticated")
		h.ErrorHandler(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Parse form
	if err := r.ParseForm(); err != nil {
		log.Printf("Form parse error: %v", err)
		h.ErrorHandler(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	// Get and validate data
	postID := r.FormValue("post_id")
	content := strings.TrimSpace(r.FormValue("content"))

	// Check if comment is not empty
	if content == "" {
		log.Printf("Empty comment content")
		h.ErrorHandler(w, "Comment cannot be empty", http.StatusBadRequest)
		return
	}

	// Check if postID is a valid number
	pid, err := strconv.ParseInt(postID, 10, 64)
	if err != nil {
		log.Printf("Invalid post ID: %v", err)
		h.ErrorHandler(w, "Invalid post ID", http.StatusBadRequest)
		return
	}

	// Check if post exists in the db
	var exists bool
	err = h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM posts WHERE id = ?)", pid).Scan(&exists)
	if err != nil {
		log.Printf("Error checking post existence: %v", err)
		h.ErrorHandler(w, "Database error", http.StatusInternalServerError)
		return
	}
	if !exists {
		log.Printf("Post %d does not exist", pid)
		h.ErrorHandler(w, "Post not found", http.StatusNotFound)
		return
	}

	// Create comment with correct timestamp
	now := time.Now().In(location)
	log.Printf("Adding comment: postID=%s, userID=%d, content=%s, username=%s, time=%v",
		postID, user.ID, content, user.Username, now)

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		log.Printf("Error starting transaction: %v", err)
		h.ErrorHandler(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	//inserting the comment into the database
	result, err := tx.Exec(`
		INSERT INTO comments (post_id, user_id, content, username, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, pid, user.ID, content, user.Username, now)

	if err != nil {
		log.Printf("Error creating comment: %v", err)
		h.ErrorHandler(w, "Error creating comment", http.StatusInternalServerError)
		return
	}

	//committing the transaction
	if err := tx.Commit(); err != nil {
		log.Printf("Error committing transaction: %v", err)
		h.ErrorHandler(w, "Database error", http.StatusInternalServerError)
		return
	}

	//getting the ID of the comment
	commentID, err := result.LastInsertId()
	if err != nil {
		log.Printf("Error getting comment ID: %v", err)
	} else {
		log.Printf("Created comment %d for post %d", commentID, pid)
	}

	//checking if the comment was successfully added into the database
	var count int
	err = h.db.QueryRow("SELECT COUNT(*) FROM comments WHERE id = ?", commentID).Scan(&count)
	if err != nil {
		log.Printf("Error verifying comment: %v", err)
	} else {
		log.Printf("Comment verification: found %d comments with ID %d", count, commentID)
	}

	//redirecting the user back to the post page
	http.Redirect(w, r, "/post/"+postID, http.StatusSeeOther)
}

// handling the deletion of comments
func (h *Handler) DeleteComment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.ErrorHandler(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	//checking if the user is authenticated
	user := h.GetSessionUser(r)
	if user == nil {
		h.ErrorHandler(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	//recieving the comment ID and post ID from the HTTP POST request
	commentID := r.FormValue("comment_id")
	postID := r.FormValue("post_id")

	//query is searching for the user ID of the comment
	var userID int64
	err := h.db.QueryRow("SELECT user_id FROM comments WHERE id = ?", commentID).Scan(&userID)
	if err != nil {
		log.Printf("Error checking comment ownership: %v", err)
		h.ErrorHandler(w, "Comment not found", http.StatusNotFound)
		return
	}

	//only the owner of the comment or an admin can delete the comment
	if userID != user.ID && !user.IsAdmin {
		h.ErrorHandler(w, "Not authorized to delete this comment", http.StatusForbidden)
		return
	}

	//Disabling the comment in the database
	_, err = h.db.Exec("UPDATE comments SET is_deleted = TRUE WHERE id = ?", commentID)
	if err != nil {
		log.Printf("Error disabling comment: %v", err)
		h.ErrorHandler(w, "Error disabling comment", http.StatusInternalServerError)
		return
	}

	//redirecting the user back to the post page
	http.Redirect(w, r, "/post/"+postID, http.StatusSeeOther)
}

// a function to edit the comment
func (h *Handler) EditComment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.ErrorHandler(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	//checking if the user is authenticated
	user := h.GetSessionUser(r)
	if user == nil {
		h.ErrorHandler(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if err := r.ParseForm(); err != nil {
		h.ErrorHandler(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	commentID := r.FormValue("comment_id")
	postID := r.FormValue("post_id")
	newContent := r.FormValue("content")

	//query is searching for the user ID of the comment
	var userID int64
	err := h.db.QueryRow("SELECT user_id FROM comments WHERE id = ?", commentID).Scan(&userID)
	if err != nil {
		log.Printf("Error checking comment ownership: %v", err)
		h.ErrorHandler(w, "Comment not found", http.StatusNotFound)
		return
	}

	//only the owner of the comment or an admin can edit the comment
	if userID != user.ID && !user.IsAdmin {
		h.ErrorHandler(w, "Not authorized to edit this comment", http.StatusForbidden)
		return
	}

	//updating the comment in the database
	_, err = h.db.Exec("UPDATE comments SET content = ? WHERE id = ?", newContent, commentID)
	if err != nil {
		log.Printf("Error updating comment: %v", err)
		h.ErrorHandler(w, "Error updating comment", http.StatusInternalServerError)
		return
	}

	//redirecting the user back to the post page
	http.Redirect(w, r, "/post/"+postID, http.StatusSeeOther)
}
