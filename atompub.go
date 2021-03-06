package main

import (
	"database/sql"
	"encoding/xml"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/ironcamel/go.atom"
	_ "github.com/lib/pq"
	"github.com/satori/go.uuid"
	"github.com/unrolled/render" // or "gopkg.in/unrolled/render.v1"
	yaml "gopkg.in/yaml.v2"
)

var r = render.New()
var db *sql.DB

func main() {
	port := flag.Int("port", 8000, "the port")
	configPath := flag.String("config", "./config.yaml", "path to config file")
	flag.Parse()

	yamlContent, err := ioutil.ReadFile(*configPath)
	if err != nil {
		log.Fatal("Could not read config file: ", err)
	}
	config := make(map[string]string)
	if err = yaml.Unmarshal(yamlContent, config); err != nil {
		log.Fatal("Could not parse config file: ", err)
	}
	if db, err = sql.Open("postgres", config["dsn"]); err != nil {
		log.Fatal("Could not open db: ", err)
	}

	fmt.Println(time.Now().Format(time.RFC3339))

	router := mux.NewRouter()
	router.HandleFunc("/feeds/{feed}", getFeed).Methods("GET")
	router.HandleFunc("/feeds/{feed}", addEntry).Methods("POST")
	http.ListenAndServe(fmt.Sprint(":", *port), router)
}

func getFeed(w http.ResponseWriter, req *http.Request) {
	feedTitle := mux.Vars(req)["feed"]
	feedPtr, err := findFeed(feedTitle)
	if err != nil {
		if err == sql.ErrNoRows {
			r.Text(w, 404, "No such feed")
			return
		} else {
			r.Text(w, 500, fmt.Sprint("Failed to get feed: ", err))
			return
		}
	}
	if err := appendEntries(feedPtr); err != nil {
		r.Text(w, 500, fmt.Sprint("Failed to construct feed: ", err))
		return
	}
	namespace := "http://www.w3.org/2005/Atom"
	feedPtr.Namespace = &namespace
	contentType := "application/atom+xml; type=feed;charset=UTF-8"
	w.Header().Set("Content-Type", contentType)
	res, err := xml.Marshal(feedPtr)
	fmt.Printf("%+v\n", feedPtr.XMLName)
	w.Write(res)
}

func appendEntries(feed *atom.XMLFeed) error {
	rows, err := db.Query(
		`select id, title, content from atom_entry where feed_title = $1`,
		feed.Title.Raw,
	)
	defer rows.Close()
	if err != nil {
		return err
	}
	var entries []atom.XMLEntry
	for rows.Next() {
		var id, title, content string
		if err := rows.Scan(&id, &title, &content); err != nil {
			return err
		}
		xmlTitle := atom.XMLTitle{Raw: title}
		xmlContent := atom.XMLEntryContent{Raw: content}
		entry := atom.XMLEntry{Id: &id, Title: &xmlTitle, Content: &xmlContent}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	feed.Entries = entries
	return nil
}

func addEntry(w http.ResponseWriter, req *http.Request) {
	entry, err := atom.DecodeEntry(req.Body)
	if err != nil {
		r.Text(w, 400, fmt.Sprint("could not parse xml: ", err))
		return
	}
	feedTitle := mux.Vars(req)["feed"]
	if _, err := insertEntry(entry, feedTitle); err != nil {
		r.Text(w, 500, fmt.Sprint("failed to save entry: ", err))
		return
	}
	r.XML(w, 201, entry)
}

func insertEntry(entry *atom.XMLEntry, feedTitle string) (*sql.Result, error) {
	_, err := findFeed(feedTitle)
	if err != nil {
		if err == sql.ErrNoRows {
			if _, err = insertFeed(feedTitle); err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	id := genId()
	titleType := "text"
	if entry.Title.Type != nil {
		titleType = *entry.Title.Type
	}
	contentType := "text"
	if entry.Content.Type != nil {
		contentType = *entry.Content.Type
	}
	result, err := db.Exec(
		`insert into atom_entry
		(id, feed_title, title, title_type, content, content_type)
		values ($1,$2,$3,$4,$5,$6)`,
		id, feedTitle, entry.Title.Raw, titleType,
		entry.Content.Raw, contentType,
	)
	return &result, err
}

func findFeed(title string) (*atom.XMLFeed, error) {
	row := db.QueryRow(`select id from atom_feed where title = $1`, title)
	var id string
	if err := row.Scan(&id); err != nil {
		return nil, err
	}
	feed := atom.XMLFeed{Id: &id}
	xmlTitle := atom.XMLTitle{Raw: title}
	feed.Title = &xmlTitle
	return &feed, nil
}

func insertFeed(title string) (string, error) {
	id := genId()
	_, err := db.Exec(
		`insert into atom_feed (id, title) values ($1, $2)`, id, title)
	return id, err
}

func genId() string {
	return fmt.Sprintf("urn:uuid:%s", uuid.NewV4().String())
}
