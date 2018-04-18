package main

import (
	"context"
	"encoding/xml"
	"fmt"
	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/postgres"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
	"golang.org/x/crypto/bcrypt"
	"html/template"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strconv"
)

type SearchPage struct {
	Query   string
	Results []Result
}

type Result struct {
	Title  string `xml:"title,attr"`
	Author string `xml:"author,attr"`
	Year   int    `xml:"hyr,attr"`
	ID     string `xml:"owi,attr"`
}

type Book struct {
	gorm.Model
	Title          string
	Author         string
	OWI            string
	Classification string
	UserID         uint
}

type User struct {
	gorm.Model
	Username string
	Password []byte
	Books    []Book
}

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	dialect := "postgres"
	if dbURL == "" {
		dbURL = "test.db"
		dialect = "sqlite3"
	}
	db, err := gorm.Open(dialect, dbURL)
	if err != nil {
		panic("failed to connect database")
	}
	defer db.Close()
	db.AutoMigrate(&Book{}, &User{})

	withAuth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie("user")
			if err != nil || cookie.Value == "" {
				http.Redirect(w, r, "/login", 302)
				return
			}

			var user User
			db.Find(&user, cookie.Value)
			if user.ID == 0 {
				http.Redirect(w, r, "/login", 302)
				return
			}

			next.ServeHTTP(w,
				r.WithContext(context.WithValue(r.Context(), "user", user)))
		}
	}

	libraryTemplates := template.Must(
		template.ParseFiles("templates/layout.html", "templates/library.html"))
	searchTemplates := template.Must(
		template.ParseFiles("templates/layout.html", "templates/search.html"))
	loginTemplate := template.Must(template.ParseFiles("templates/login.html"))

	http.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if err := loginTemplate.ExecuteTemplate(w, "login.html", nil); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	http.HandleFunc("/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var user User
		db.Where("username = ?", r.FormValue("username")).First(&user)
		if bcrypt.CompareHashAndPassword(user.Password,
			[]byte(r.FormValue("password"))) == nil {
			http.SetCookie(w, &http.Cookie{
				Name:  "user",
				Value: strconv.Itoa(int(user.ID)),
				Path:  "/",
			})
		}
		http.Redirect(w, r, "/", 302)
	})

	http.HandleFunc("/auth/register",
		func(w http.ResponseWriter, r *http.Request) {
			pwHash, _ := bcrypt.GenerateFromPassword(
				[]byte(r.FormValue("password")), bcrypt.DefaultCost)
			user := User{Username: r.FormValue("username"), Password: pwHash}
			db.Create(&user)
			http.SetCookie(w, &http.Cookie{Name: "user", Value: strconv.Itoa(int(user.ID)), Path: "/"})
			http.Redirect(w, r, "/", 302)
		})

	http.HandleFunc("/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "user", Value: "", Path: "/"})
		http.Redirect(w, r, "/login", 302)
	})

	http.Handle("/removebook", withAuth(func(w http.ResponseWriter, r *http.Request) {
		var book Book
		db.Find(&book, r.FormValue("bookId"))
		db.Delete(book)

		http.Redirect(w, r, "/", 302)
	}))

	http.Handle("/addbook", withAuth(func(w http.ResponseWriter, r *http.Request) {
		res, e := find(r.FormValue("bookId"))
		if e != nil {
			http.Error(w, e.Error(), http.StatusInternalServerError)
		}
		db.Create(&Book{
			Title:          res.BookData.Title,
			Author:         res.BookData.Author,
			OWI:            res.BookData.ID,
			Classification: res.Classification.MostPopular,
			UserID:         r.Context().Value("user").(User).ID,
		})

		http.Redirect(w, r, "/", 302)
	}))

	http.HandleFunc("/search", withAuth(func(w http.ResponseWriter, r *http.Request) {
		results, e := search(r.FormValue("search"))
		if e != nil {
			http.Error(w, e.Error(), http.StatusInternalServerError)
		}

		p := SearchPage{Query: r.FormValue("search"), Results: results}
		if err := searchTemplates.ExecuteTemplate(w, "layout", p); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))

	http.HandleFunc("/", withAuth(func(w http.ResponseWriter, r *http.Request) {
		var p struct{ Books []Book }

		order := r.FormValue("sort")
		if order != "title" && order != "author" && order != "classification" {
			order = "title"
		}
		where := ""
		if filterInt, err := strconv.Atoi(r.FormValue("filter")); err == nil {
			where = "classification BETWEEN " + r.FormValue("filter") +
				" AND " + strconv.Itoa(filterInt+100)
		}

		user := r.Context().Value("user").(User)
		db.Model(&user).Order(order).Where(where).Related(&p.Books)

		if err := libraryTemplates.ExecuteTemplate(w, "layout", p); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))

	port := os.Getenv("PORT")
	if port == "" {
		port = "4000"
	}
	fmt.Println(http.ListenAndServe(":"+port, nil))
}

func search(query string) ([]Result, error) {
	var response struct {
		Results []Result `xml:"works>work"`
	}

	body, err := fetch("title=" + url.QueryEscape(query))

	if err != nil {
		return []Result{}, err
	}

	err = xml.Unmarshal(body, &response)
	return response.Results, err
}

type BookResponse struct {
	BookData struct {
		Title  string `xml:"title,attr"`
		Author string `xml:"author,attr"`
		ID     string `xml:"owi,attr"`
	} `xml:"work"`
	Classification struct {
		MostPopular string `xml:"sfa,attr"`
	} `xml:"recommendations>ddc>mostPopular"`
}

func find(id string) (BookResponse, error) {
	var response BookResponse
	body, err := fetch("owi=" + url.QueryEscape(id))

	if err != nil {
		return BookResponse{}, err
	}

	err = xml.Unmarshal(body, &response)
	return response, err
}

func fetch(q string) ([]byte, error) {
	var resp *http.Response
	var err error
	url := "http://classify.oclc.org/classify2/Classify?summary=true&" + q

	if resp, err = http.Get(url); err != nil {
		return []byte{}, err
	}

	defer resp.Body.Close()

	return ioutil.ReadAll(resp.Body)
}
