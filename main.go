package main

import (
	"encoding/xml"
	"fmt"
	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
	"html/template"
	"io/ioutil"
	"net/http"
	"net/url"
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
}

func main() {
	db, err := gorm.Open("sqlite3", "test.db")
	if err != nil {
		panic("failed to connect database")
	}
	defer db.Close()
	db.AutoMigrate(&Book{})

	templates := template.Must(template.ParseFiles("templates/index.html"))

	http.HandleFunc("/addbook", func(w http.ResponseWriter, r *http.Request) {
		res, e := find(r.FormValue("bookId"))
		if e != nil {
			http.Error(w, e.Error(), http.StatusInternalServerError)
		}
		db.Create(&Book{
			Title:          res.BookData.Title,
			Author:         res.BookData.Author,
			OWI:            res.BookData.ID,
			Classification: res.Classification.MostPopular,
		})

		if err := templates.ExecuteTemplate(w, "index.html", nil); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	http.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		results, e := search(r.FormValue("search"))
		if e != nil {
			http.Error(w, e.Error(), http.StatusInternalServerError)
		}

		p := SearchPage{Query: r.FormValue("search"), Results: results}
		if err := templates.ExecuteTemplate(w, "index.html", p); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if err := templates.ExecuteTemplate(w, "index.html", nil); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	fmt.Println(http.ListenAndServe(":4000", nil))
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
