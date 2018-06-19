package main

import (
	"net/http"
	"fmt"
	"gopkg.in/square/go-jose.v2/json"
	"io/ioutil"
	"errors"
	"github.com/gorilla/mux"
	"strconv"
	"log"
	"github.com/coreos/bbolt"
	"time"
	"encoding/binary"
)

const issApi = "http://api.open-notify.org/iss-pass.json?lat=%f&lon=%f&n=2"

var db *bolt.DB

type Loc struct {
	Lat float64
	Long float64
}

type Pass struct {
	Duration int `json:"duration"`
	RiseTime int64 `json:"risetime"`
}

type apiRes struct {
	Message string `json:"message"`
	Reason string `json:"reason"`
	Response []*Pass `json:"response"`
}

func getNextPass(loc *Loc) (*Pass, error) {
	res, err := http.Get(fmt.Sprintf(issApi, loc.Lat, loc.Long))
	if err != nil {
		return nil, err
	}

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	data := &apiRes{}
	err = json.Unmarshal(body, data)
	if err != nil {
		return nil, err
	}

	if data.Message != "success" {
		return nil, errors.New(data.Reason)
	}

	if len(data.Response) == 0 {
		return nil, errors.New("no passes")
	}

	return data.Response[0], nil
}

func handleLocUpdate(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	lat, err := strconv.ParseFloat(r.Form.Get("lat"), 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		log.Println(err)
		return
	}

	long, err := strconv.ParseFloat(r.Form.Get("long"), 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		log.Println(err)
		return
	}

	loc := &Loc{lat, long}

	db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("Locations"))

		data, err := json.Marshal(loc)
		if err != nil {
			return err
		}

		err = b.Put([]byte("current"), data)
		return err
	})
}

func checkIsOver() {
	c := time.NewTicker(time.Second)
	for range c.C {
		curLoc := &Loc{}
		err := db.View(func(tx *bolt.Tx) error {
			b := tx.Bucket([]byte("Locations"))

			data := b.Get([]byte("current"))

			err := json.Unmarshal(data, curLoc)
			return err
		})
		if err != nil {
			log.Println(err)
			continue
		}

		pass, err := getNextPass(curLoc)
		if err != nil {
			log.Println(err)
			continue
		}
		log.Println(pass)

		var lastTweet uint64
		db.View(func(tx *bolt.Tx) error {
			b := tx.Bucket([]byte("Locations"))

			data := b.Get([]byte("tweeted"))

			lastTweet = binary.LittleEndian.Uint64(data)
			return nil
		})

		if uint64(pass.RiseTime) > lastTweet {
			if (pass.RiseTime <= time.Now().Unix()) && (pass.RiseTime+int64(pass.Duration) >= time.Now().Unix()) {
				err := db.Update(func(tx *bolt.Tx) error {
					b := tx.Bucket([]byte("Locations"))

					bs := make([]byte, 8)
					binary.LittleEndian.PutUint64(bs, uint64(pass.RiseTime))
					err := b.Put([]byte("tweeted"), bs)
					return err
				})
				if err != nil {
					log.Println(err)
					continue
				}

				log.Println("ISS is over Ben")
			}
		}
	}
}

func main()  {
	var err error
	db, err = bolt.Open("data.db", 0600, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte("Locations"))
		if err != nil {
			return fmt.Errorf("create bucket: %s", err)
		}
		return nil
	})

	go checkIsOver()

	r := mux.NewRouter()

	r.Methods("POST").Path("/loc-push").HandlerFunc(handleLocUpdate)

	http.ListenAndServe(":8123", r)
}
