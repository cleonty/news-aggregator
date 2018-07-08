package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/antchfx/htmlquery"
	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/net/html"
)

const databseFile = "./news.db"
const parsingRulesFile = "./rules.json"

type ExtractRule struct {
	XPathExpr string `json:"expr"`
	Attribute string `json:"attr,omitempty"`
}

type ParsingRule struct {
	Interval           uint        `json:"intervalMinutes"`
	URL                string      `json:"url"`
	NewsNodesXPathExpr string      `json:"newsNodesExpr"`
	LinkRule           ExtractRule `json:"linkRule"`
	TitleRule          ExtractRule `json:"titleRule"`
}

// NewsItem represnts a news
type NewsItem struct {
	Link  string `json:"link"`
	Title string `json:"title"`
}

type NewsApp struct {
	db           *sql.DB
	server       *http.Server
	parsingRules []*ParsingRule
}

func (app *NewsApp) readParsingRules() error {
	data, err := ioutil.ReadFile(parsingRulesFile)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, &app.parsingRules); err != nil {
		return fmt.Errorf("error while reading parsing rules: %v", err)
	}
	return nil
}

func (app *NewsApp) loadNewsList(rule *ParsingRule) ([]NewsItem, error) {
	var items []NewsItem
	doc, err := htmlquery.LoadURL(rule.URL)
	if err != nil {
		return nil, err
	}
	for _, node := range htmlquery.Find(doc, rule.NewsNodesXPathExpr) {
		link := extractEntity(node, &rule.LinkRule)
		title := extractEntity(node, &rule.TitleRule)
		link, err = convertToAbsURL(rule.URL, link)
		if err != nil {
			return nil, fmt.Errorf("error converting link url %s to absolute url using base url %s: %v", link, rule.URL, err)
		}
		item := NewsItem{
			Link:  link,
			Title: title,
		}
		items = append(items, item)
	}
	return items, nil
}

func (app *NewsApp) searchHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		log.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	query := r.Form.Get("q")
	items, err := app.getNews(query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data, err := json.MarshalIndent(items, "", "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-type", "application/json")
	fmt.Fprintf(w, "%s\n", data)
}

func (app *NewsApp) updateNewsPeriodically(rule *ParsingRule) {
	app.updateNews(rule)
	ticker := time.NewTicker(time.Duration(rule.Interval) * time.Minute)
	for {
		select {
		case <-ticker.C:
			app.updateNews(rule)
		}
	}
}

func (app *NewsApp) updateNews(rule *ParsingRule) {
	items, err := app.loadNewsList(rule)
	if err != nil {
		log.Fatal(err)
	}
	for _, item := range items {
		err = app.insertNewsItem(&item)
		if err != nil {
			log.Println(err)
		}
	}
}

func (app *NewsApp) startUpdaters() {
	for _, rule := range app.parsingRules {
		go app.updateNewsPeriodically(rule)
	}
}

func (app *NewsApp) openDatabase() error {
	const newsStatement = `
		CREATE TABLE IF NOT EXISTS 'news' (
		'id' INTEGER PRIMARY KEY AUTOINCREMENT,
		'link' VARCHAR(1024) UNIQUE NOT NULL,
		'title' VARCHAR(1024) NOT NULL,
		'timestamp' DATETIME DEFAULT CURRENT_TIMESTAMP)`
	db, err := sql.Open("sqlite3", databseFile)
	if err != nil {
		return err
	}
	_, err = db.Exec(newsStatement)
	if err != nil {
		db.Close()
		return err
	}
	app.db = db
	return nil
}

func (app *NewsApp) getNews(query string) ([]NewsItem, error) {
	items := make([]NewsItem, 0)
	var statement string
	if query != "" {
		statement = "SELECT link, title FROM news WHERE instr(title, ?) <> 0 ORDER BY timestamp DESC"
	} else {
		statement = "SELECT link, title FROM news ORDER BY timestamp DESC"
	}
	rows, err := app.db.Query(statement, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item NewsItem
		if err := rows.Scan(&item.Link, &item.Title); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (app *NewsApp) insertNewsItem(item *NewsItem) error {
	_, err := app.db.Exec("INSERT INTO news(link, title) values(?, ?)", item.Link, item.Title)
	if err != nil {
		return fmt.Errorf("Insert failed for link='%s', title='%s': %v", item.Link, item.Title, err)
	}
	return nil
}

func NewNewsApp() *NewsApp {
	return &NewsApp{}
}

func (app *NewsApp) Start() error {
	if err := app.readParsingRules(); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(app.parsingRules, "", "  ")
	log.Printf("parsing rules: %s\n", string(data))
	if err := app.openDatabase(); err != nil {
		return err
	}
	app.startUpdaters()
	mux := http.NewServeMux()
	mux.HandleFunc("/news/", app.searchHandler)
	mux.Handle("/", http.FileServer(http.Dir("./client/dist/client")))
	if err := http.ListenAndServe(":8080", mux); err != nil {
		return err
	}
	return nil
}

func extractEntity(parentNode *html.Node, rule *ExtractRule) string {
	var result string
	node := htmlquery.FindOne(parentNode, rule.XPathExpr)
	if node != nil {
		if rule.Attribute != "" {
			result = htmlquery.SelectAttr(node, rule.Attribute)
		} else {
			result = htmlquery.InnerText(node)
		}
	}
	if result == "" {
		data, _ := json.MarshalIndent(rule, "", "")
		log.Printf("The rule %s might be not working because returns empty result", data)
	}
	return result
}

func convertToAbsURL(baseURL string, linkURL string) (string, error) {
	url, err := url.Parse(linkURL)
	if err != nil {
		return "", err
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	if !url.IsAbs() {
		return base.ResolveReference(url).String(), nil
	}
	return linkURL, nil
}

func main() {
	app := NewNewsApp()
	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
