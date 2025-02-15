package handlers

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
)

// HomeHandler is a handler for the home page
func (h *Handler) HomeHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		h.ErrorHandler(w, "Page not found", http.StatusNotFound)
		return
	}

	//calling out the getCategories function to get all the categories
	categories, err := h.getCategories()
	if err != nil {
		log.Printf("Error getting categories: %v", err)
		h.ErrorHandler(w, "Server error", http.StatusInternalServerError)
		return
	}

	//getting the user from the session
	user := h.GetSessionUser(r)

	//getting the category ID from the URL
	categoryIDStr := r.URL.Query().Get("category")
	var selectedCategoryID int64
	if categoryIDStr != "" {
		var err error
		//if there is a category ID, convert it to an int64. If it fails, set the category ID to 0
		selectedCategoryID, err = strconv.ParseInt(categoryIDStr, 10, 64)
		if err != nil {
			log.Printf("Invalid category ID: %v", err)
			selectedCategoryID = 0
		}
	}

	//checking if the user has selected to see only their posts
	showMyPosts := r.URL.Query().Get("my_posts") == "true"
	//checking if the user has selected to see only their liked posts
	showLikedPosts := r.URL.Query().Get("liked_posts") == "true"

	//calling out the getPosts function to get all the posts
	posts := getPosts(h.db)

	//if the user is logged in
	if user != nil {
		//for each post, check if the user has liked or disliked the post
		for i := range posts {
			var reactionType string
			err := h.db.QueryRow(`
				SELECT type FROM reactions 
				WHERE user_id = ? AND post_id = ?`,
				user.ID, posts[i].ID,
			).Scan(&reactionType)

			if err == nil {
				posts[i].UserLiked = reactionType == "like"
				posts[i].UserDisliked = reactionType == "dislike"
			}
		}
	}

	//collecting all the data into a struct
	data := TemplateData{
		User:             user,
		Posts:            posts,
		Categories:       categories,
		SelectedCategory: selectedCategoryID,
		ShowMyPosts:      showMyPosts,
		ShowLikedPosts:   showLikedPosts,
	}

	if err := h.templates.ExecuteTemplate(w, "index.html", data); err != nil {
		log.Printf("Template error: %v", err)
		h.ErrorHandler(w, "Error rendering page", http.StatusInternalServerError)
	}
}

//query from the database to get all the posts
func getPosts(db *sql.DB) []Post {

//left join: joins the posts with categories, adds category names and counts the comments
	query := `
		SELECT p.id, p.title, p.content, p.username, p.created_at, p.user_id,
			   p.likes, p.dislikes, GROUP_CONCAT(DISTINCT c.name) as categories,
			   COUNT(DISTINCT cm.id) as comment_count
		FROM posts p
		LEFT JOIN post_categories pc ON p.id = pc.post_id
		LEFT JOIN categories c ON pc.category_id = c.id
		LEFT JOIN comments cm ON p.id = cm.post_id
		GROUP BY p.id, p.title, p.content, p.username, p.created_at, p.user_id,
				 p.likes, p.dislikes
		ORDER BY p.created_at DESC
	`
	rows, err := db.Query(query)
	if err != nil {
		log.Printf("Query error: %v", err)
		return nil
	}
	//close the rows when the function ends
	defer rows.Close()


	var posts []Post
	for rows.Next() {
		var p Post
		var categories sql.NullString //sql.NullString is used to handle NULL values from the database
		err := rows.Scan(
			&p.ID, &p.Title, &p.Content, &p.Username, &p.CreatedAt, &p.UserID,
			&p.Likes, &p.Dislikes, &categories, &p.CommentCount,
		)
		if err != nil {
			log.Printf("Scan error: %v", err)
			continue
		}
		//if the categories are valid, split the string by comma into a slice
		if categories.Valid {
			p.Categories = strings.Split(categories.String, ",")
		}
		//log the post details
		log.Printf("Post: ID=%d, Title=%s, Comments=%d, Categories=%v",
			p.ID, p.Title, p.CommentCount, p.Categories)
			//append the post to the posts slice
		posts = append(posts, p)
	}
	return posts
}

func (h *Handler) CategoryHandler(w http.ResponseWriter, r *http.Request) {
	//recieve the category ID from the URL
	categoryIDStr := r.URL.Path[len("/category/"):]
	log.Printf("Accessing category with ID: %s", categoryIDStr)

	//if the category ID is empty, return a 404 error
	if categoryIDStr == "" {
		log.Printf("Empty category ID")
		h.ErrorHandler(w, "Category not found", http.StatusNotFound)
		return
	}

	//convert the category ID to an int64 because it is a string
	categoryID, err := strconv.ParseInt(categoryIDStr, 10, 64)
	if err != nil {
		log.Printf("Invalid category ID: %v", err)
		h.ErrorHandler(w, "Invalid category ID", http.StatusBadRequest)
		return
	}

	//query the database to get the category with the given ID
	var category Category
	err = h.db.QueryRow("SELECT id, name, description FROM categories WHERE id = ?", categoryID).
		Scan(&category.ID, &category.Name, &category.Description)
	if err != nil {
		log.Printf("Error getting category: %v", err)
		h.ErrorHandler(w, "Category not found", http.StatusNotFound)
		return
	}
	log.Printf("Found category: %s", category.Name)

	//gets the user who has a session right now
	user := h.GetSessionUser(r)

	//query to get the posts with the given category ID
	query := `
		SELECT p.id, p.title, p.content, p.username, p.created_at, p.user_id,
			   COUNT(DISTINCT cm.id) as comment_count,
			   EXISTS(
				   SELECT 1 FROM reactions r 
				   WHERE r.post_id = p.id 
				   AND r.user_id = ? 
				   AND r.type = 'like'
			   ) as user_liked
		FROM posts p
		INNER JOIN post_categories pc ON p.id = pc.post_id
		LEFT JOIN comments cm ON p.id = cm.post_id
		WHERE pc.category_id = ?
		GROUP BY p.id
		ORDER BY p.created_at DESC
	`

	//creates an user ID (0, if the user is not logged in)
	var userID int64
	if user != nil {
		userID = user.ID
	}

	//starts the query to get the posts with the given category ID
	rows, err := h.db.Query(query, userID, categoryID)
	if err != nil {
		log.Printf("Error getting posts: %v", err)
		h.ErrorHandler(w, "Error getting posts", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	//collecting all the posts into a slice
	var posts []Post
	for rows.Next() {
		var p Post
		err := rows.Scan(
			&p.ID, &p.Title, &p.Content, &p.Username, &p.CreatedAt, &p.UserID,
			&p.CommentCount, &p.UserLiked,
		)
		if err != nil {
			log.Printf("Error scanning post: %v", err)
			continue
		}
		posts = append(posts, p)
	}

	//collecting all the data into a struct
	data := TemplateData{
		Title:    category.Name,
		User:     user,
		Category: &category,
		Posts:    posts,
	}

	//render the category.html template with the data
	h.templates.ExecuteTemplate(w, "category.html", data)
}


//a function to prepare the data for the post.html template
func (h *Handler) GetPost(w http.ResponseWriter, r *http.Request) {
	//get the post ID from the URL by cutting the /post/ from the URL
	postID := r.URL.Path[len("/post/"):]
	log.Printf("Getting post with ID: %s", postID)
	//recieve the category ID from the URL
	catId := r.URL.Query().Get("cat")

	Category := Category{}
	Category.ID, _ = strconv.ParseInt(catId, 10, 64)

	//get the user from the session
	user := h.GetSessionUser(r)

	//calling out the getPostByID function to get the post with the given ID
	post, err := h.getPostByID(postID)
	if err != nil {
		log.Printf("Error getting post: %v", err)
		h.ErrorHandler(w, "Post not found", http.StatusNotFound)
		return
	}

	//if user has a session, check if the user has liked or disliked the post
	if user != nil {
		post.UserLiked = h.hasUserReaction(user.ID, post.ID, "like")
		post.UserDisliked = h.hasUserReaction(user.ID, post.ID, "dislike")
	}

	//calling out the getComments function to get the comments of the post
	comments, err := h.getComments(post.ID)
	if err != nil {
		log.Printf("Error getting comments: %v", err)
		h.ErrorHandler(w, "Error loading comments", http.StatusInternalServerError)
		return
	}
	log.Printf("Found %d comments for post %d", len(comments), post.ID)

	//checking if the user has liked or disliked the comments
	if user != nil {
		for _, comment := range comments {
			comment.UserLiked = h.hasCommentReaction(user.ID, comment.ID, "like")
			comment.UserDisliked = h.hasCommentReaction(user.ID, comment.ID, "dislike")
		}
	}

	// prepare data for the template
	var commentDataList []CommentData
	for _, comment := range comments {
		//each comment will have a user and a post
		commentData := CommentData{
			Comment: comment,
			User:    user,
			Post:    post,
		}
		commentDataList = append(commentDataList, commentData)
	}

	//collecting all the data into a struct
	data := TemplateData{
		Title:           post.Title,
		Post:            post,
		User:            user,
		Comments:        comments,
		CommentDataList: commentDataList,
		Category:        &Category,
	}

	//render the post.html template with the data
	if err := h.templates.ExecuteTemplate(w, "post.html", data); err != nil {
		log.Printf("Template error: %v", err)
		h.ErrorHandler(w, "Error rendering page", http.StatusInternalServerError)
	}
}

//a function to get a specific post from the database
func (h *Handler) getPostByID(postID string) (*Post, error) {
	var post Post
	err := h.db.QueryRow(`
		SELECT p.id, p.user_id, p.title, p.content, p.created_at,
			   u.username,
			   COUNT(DISTINCT CASE WHEN r.type = 'like' THEN r.id END) as likes,
			   COUNT(DISTINCT CASE WHEN r.type = 'dislike' THEN r.id END) as dislikes
		FROM posts p
		JOIN users u ON p.user_id = u.id
		LEFT JOIN reactions r ON p.id = r.post_id
		WHERE p.id = ?
		GROUP BY p.id, p.user_id, p.title, p.content, p.created_at, u.username
	`, postID).Scan(
		&post.ID, &post.UserID, &post.Title, &post.Content, &post.CreatedAt,
		&post.Username, &post.Likes, &post.Dislikes,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("post not found: %s", postID)
		}
		return nil, err
	}

	//recieve the categories of the post
	post.Categories, err = h.getPostCategories(post.ID)
	if err != nil {
		return nil, err
	}

	//recieve the comment count of the post
	var commentCount int
	err = h.db.QueryRow(`
		SELECT COUNT(*) 
		FROM comments 
		WHERE post_id = ?
	`, post.ID).Scan(&commentCount)
	if err != nil {
		return nil, err
	}
	post.CommentCount = commentCount

	return &post, nil
}

// Add a new method to get comments
func (h *Handler) getComments(postID int64) ([]*Comment, error) {
	rows, err := h.db.Query(`
		SELECT c.id, c.user_id, c.content, c.created_at, c.username,
			   COUNT(CASE WHEN r.type = 'like' THEN 1 END) as likes,
			   COUNT(CASE WHEN r.type = 'dislike' THEN 1 END) as dislikes
		FROM comments c
		LEFT JOIN reactions r ON c.id = r.comment_id
		WHERE c.post_id = ?
		GROUP BY c.id, c.user_id, c.content, c.created_at, c.username
		ORDER BY c.created_at DESC
	`, postID)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	//collecting all the comments into a slice
	var comments []*Comment
	for rows.Next() {
		var c Comment
		err := rows.Scan(
			&c.ID, &c.UserID, &c.Content, &c.CreatedAt, &c.Username,
			&c.Likes, &c.Dislikes,
		)
		if err != nil {
			return nil, err
		}
		comments = append(comments, &c)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return comments, nil
}

// Add a new method to check reactions on comments
func (h *Handler) hasCommentReaction(userID int64, commentID int64, reactionType string) bool {
	var exists bool
	//checking if the db has a reaction with the same user, comment and type
	//EXISTS returns true if the subquery returns one or more rows
	err := h.db.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM reactions 
			WHERE user_id = ? AND comment_id = ? AND type = ?
		)
	`, userID, commentID, reactionType).Scan(&exists)

	if err != nil {
		return false
	}
	return exists
}
