package main

import (
	crand "crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/sessions"
	"github.com/jmoiron/sqlx"
	goji "goji.io"
	"goji.io/pat"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionName = "session_isucari"

	DefaultPaymentServiceURL  = "http://localhost:5555"
	DefaultShipmentServiceURL = "http://localhost:7000"

	ItemMinPrice    = 100
	ItemMaxPrice    = 1000000
	ItemPriceErrMsg = "商品価格は100ｲｽｺｲﾝ以上、1,000,000ｲｽｺｲﾝ以下にしてください"

	ItemStatusOnSale  = "on_sale"
	ItemStatusTrading = "trading"
	ItemStatusSoldOut = "sold_out"
	ItemStatusStop    = "stop"
	ItemStatusCancel  = "cancel"

	PaymentServiceIsucariAPIKey = "a15400e46c83635eb181-946abb51ff26a868317c"
	PaymentServiceIsucariShopID = "11"

	TransactionEvidenceStatusWaitShipping = "wait_shipping"
	TransactionEvidenceStatusWaitDone     = "wait_done"
	TransactionEvidenceStatusDone         = "done"

	ShippingsStatusInitial    = "initial"
	ShippingsStatusWaitPickup = "wait_pickup"
	ShippingsStatusShipping   = "shipping"
	ShippingsStatusDone       = "done"

	BumpChargeSeconds = 3 * time.Second

	ItemsPerPage        = 48
	TransactionsPerPage = 10

	BcryptCost = 10
)

var (
	templates *template.Template
	dbx       *sqlx.DB
	store     sessions.Store
)

type Config struct {
	Name string `json:"name" db:"name"`
	Val  string `json:"val" db:"val"`
}

type User struct {
	ID             int64     `json:"id" db:"id"`
	AccountName    string    `json:"account_name" db:"account_name"`
	HashedPassword []byte    `json:"-" db:"hashed_password"`
	Address        string    `json:"address,omitempty" db:"address"`
	NumSellItems   int       `json:"num_sell_items" db:"num_sell_items"`
	LastBump       time.Time `json:"-" db:"last_bump"`
	CreatedAt      time.Time `json:"-" db:"created_at"`
}

type UserSimple struct {
	ID           int64  `json:"id"`
	AccountName  string `json:"account_name"`
	NumSellItems int    `json:"num_sell_items"`
}

type Item struct {
	ID          int64     `json:"id" db:"id"`
	SellerID    int64     `json:"seller_id" db:"seller_id"`
	BuyerID     int64     `json:"buyer_id" db:"buyer_id"`
	Status      string    `json:"status" db:"status"`
	Name        string    `json:"name" db:"name"`
	Price       int       `json:"price" db:"price"`
	Description string    `json:"description" db:"description"`
	ImageName   string    `json:"image_name" db:"image_name"`
	CategoryID  int       `json:"category_id" db:"category_id"`
	CreatedAt   time.Time `json:"-" db:"created_at"`
	UpdatedAt   time.Time `json:"-" db:"updated_at"`
}

type ItemSimple struct {
	ID         int64       `json:"id"`
	SellerID   int64       `json:"seller_id"`
	Seller     *UserSimple `json:"seller"`
	Status     string      `json:"status"`
	Name       string      `json:"name"`
	Price      int         `json:"price"`
	ImageURL   string      `json:"image_url"`
	CategoryID int         `json:"category_id"`
	Category   *Category   `json:"category"`
	CreatedAt  int64       `json:"created_at"`
}

type ItemDetail struct {
	ID                        int64       `json:"id"`
	SellerID                  int64       `json:"seller_id"`
	Seller                    *UserSimple `json:"seller"`
	BuyerID                   int64       `json:"buyer_id,omitempty"`
	Buyer                     *UserSimple `json:"buyer,omitempty"`
	Status                    string      `json:"status"`
	Name                      string      `json:"name"`
	Price                     int         `json:"price"`
	Description               string      `json:"description"`
	ImageURL                  string      `json:"image_url"`
	CategoryID                int         `json:"category_id"`
	Category                  *Category   `json:"category"`
	TransactionEvidenceID     int64       `json:"transaction_evidence_id,omitempty"`
	TransactionEvidenceStatus string      `json:"transaction_evidence_status,omitempty"`
	ShippingStatus            string      `json:"shipping_status,omitempty"`
	CreatedAt                 int64       `json:"created_at"`
}

type TransactionEvidence struct {
	ID                 int64     `json:"id" db:"id"`
	SellerID           int64     `json:"seller_id" db:"seller_id"`
	BuyerID            int64     `json:"buyer_id" db:"buyer_id"`
	Status             string    `json:"status" db:"status"`
	ItemID             int64     `json:"item_id" db:"item_id"`
	ItemName           string    `json:"item_name" db:"item_name"`
	ItemPrice          int       `json:"item_price" db:"item_price"`
	ItemDescription    string    `json:"item_description" db:"item_description"`
	ItemCategoryID     int       `json:"item_category_id" db:"item_category_id"`
	ItemRootCategoryID int       `json:"item_root_category_id" db:"item_root_category_id"`
	CreatedAt          time.Time `json:"-" db:"created_at"`
	UpdatedAt          time.Time `json:"-" db:"updated_at"`
}

type Shipping struct {
	TransactionEvidenceID int64     `json:"transaction_evidence_id" db:"transaction_evidence_id"`
	Status                string    `json:"status" db:"status"`
	ItemName              string    `json:"item_name" db:"item_name"`
	ItemID                int64     `json:"item_id" db:"item_id"`
	ReserveID             string    `json:"reserve_id" db:"reserve_id"`
	ReserveTime           int64     `json:"reserve_time" db:"reserve_time"`
	ToAddress             string    `json:"to_address" db:"to_address"`
	ToName                string    `json:"to_name" db:"to_name"`
	FromAddress           string    `json:"from_address" db:"from_address"`
	FromName              string    `json:"from_name" db:"from_name"`
	ImgBinary             []byte    `json:"-" db:"img_binary"`
	CreatedAt             time.Time `json:"-" db:"created_at"`
	UpdatedAt             time.Time `json:"-" db:"updated_at"`
}

type Category struct {
	ID                 int    `json:"id" db:"id"`
	ParentID           int    `json:"parent_id" db:"parent_id"`
	CategoryName       string `json:"category_name" db:"category_name"`
	ParentCategoryName string `json:"parent_category_name,omitempty" db:"-"`
}

type reqInitialize struct {
	PaymentServiceURL  string `json:"payment_service_url"`
	ShipmentServiceURL string `json:"shipment_service_url"`
}

type resInitialize struct {
	Campaign int    `json:"campaign"`
	Language string `json:"language"`
}

type resNewItems struct {
	RootCategoryID   int          `json:"root_category_id,omitempty"`
	RootCategoryName string       `json:"root_category_name,omitempty"`
	HasNext          bool         `json:"has_next"`
	Items            []ItemSimple `json:"items"`
}

type resUserItems struct {
	User    *UserSimple  `json:"user"`
	HasNext bool         `json:"has_next"`
	Items   []ItemSimple `json:"items"`
}

type resTransactions struct {
	HasNext bool          `json:"has_next"`
	Items   []*ItemDetail `json:"items"`
}

type reqRegister struct {
	AccountName string `json:"account_name"`
	Address     string `json:"address"`
	Password    string `json:"password"`
}

type reqLogin struct {
	AccountName string `json:"account_name"`
	Password    string `json:"password"`
}

type reqItemEdit struct {
	CSRFToken string `json:"csrf_token"`
	ItemID    int64  `json:"item_id"`
	ItemPrice int    `json:"item_price"`
}

type resItemEdit struct {
	ItemID        int64 `json:"item_id"`
	ItemPrice     int   `json:"item_price"`
	ItemCreatedAt int64 `json:"item_created_at"`
	ItemUpdatedAt int64 `json:"item_updated_at"`
}

type reqBuy struct {
	CSRFToken string `json:"csrf_token"`
	ItemID    int64  `json:"item_id"`
	Token     string `json:"token"`
}

type resBuy struct {
	TransactionEvidenceID int64 `json:"transaction_evidence_id"`
}

type resSell struct {
	ID int64 `json:"id"`
}

type reqPostShip struct {
	CSRFToken string `json:"csrf_token"`
	ItemID    int64  `json:"item_id"`
}

type resPostShip struct {
	Path      string `json:"path"`
	ReserveID string `json:"reserve_id"`
}

type reqPostShipDone struct {
	CSRFToken string `json:"csrf_token"`
	ItemID    int64  `json:"item_id"`
}

type reqPostComplete struct {
	CSRFToken string `json:"csrf_token"`
	ItemID    int64  `json:"item_id"`
}

type reqBump struct {
	CSRFToken string `json:"csrf_token"`
	ItemID    int64  `json:"item_id"`
}

type resSetting struct {
	CSRFToken         string     `json:"csrf_token"`
	PaymentServiceURL string     `json:"payment_service_url"`
	User              *User      `json:"user,omitempty"`
	Categories        []Category `json:"categories"`
}

func init() {
	store = sessions.NewCookieStore([]byte("abc"))

	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	templates = template.Must(template.ParseFiles(
		"../public/index.html",
	))
}

func main() {
	host := os.Getenv("MYSQL_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("MYSQL_PORT")
	if port == "" {
		port = "3306"
	}
	_, err := strconv.Atoi(port)
	if err != nil {
		log.Fatalf("failed to read DB port number from an environment variable MYSQL_PORT.\nError: %s", err.Error())
	}
	user := os.Getenv("MYSQL_USER")
	if user == "" {
		user = "isucari"
	}
	dbname := os.Getenv("MYSQL_DBNAME")
	if dbname == "" {
		dbname = "isucari"
	}
	password := os.Getenv("MYSQL_PASS")
	if password == "" {
		password = "isucari"
	}

	dsn := fmt.Sprintf(
		"%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=true&loc=Local",
		user,
		password,
		host,
		port,
		dbname,
	)

	dbx, err = sqlx.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("failed to connect to DB: %s.", err.Error())
	}
	defer dbx.Close()

	mux := goji.NewMux()

	// API
	mux.HandleFunc(pat.Post("/initialize"), postInitialize)
	mux.HandleFunc(pat.Get("/new_items.json"), getNewItems)
	mux.HandleFunc(pat.Get("/new_items/:root_category_id.json"), getNewCategoryItems)
	mux.HandleFunc(pat.Get("/users/transactions.json"), getTransactions)
	mux.HandleFunc(pat.Get("/users/:user_id.json"), getUserItems)
	mux.HandleFunc(pat.Get("/items/:item_id.json"), getItem)
	mux.HandleFunc(pat.Post("/items/edit"), postItemEdit)
	mux.HandleFunc(pat.Post("/buy"), postBuy)
	mux.HandleFunc(pat.Post("/sell"), postSell)
	mux.HandleFunc(pat.Post("/ship"), postShip)
	mux.HandleFunc(pat.Post("/ship_done"), postShipDone)
	mux.HandleFunc(pat.Post("/complete"), postComplete)
	mux.HandleFunc(pat.Get("/transactions/:transaction_evidence_id.png"), getQRCode)
	mux.HandleFunc(pat.Post("/bump"), postBump)
	mux.HandleFunc(pat.Get("/settings"), getSettings)
	mux.HandleFunc(pat.Post("/login"), postLogin)
	mux.HandleFunc(pat.Post("/register"), postRegister)
	mux.HandleFunc(pat.Get("/reports.json"), getReports)
	// Frontend
	mux.HandleFunc(pat.Get("/"), getIndex)
	mux.HandleFunc(pat.Get("/login"), getIndex)
	mux.HandleFunc(pat.Get("/register"), getIndex)
	mux.HandleFunc(pat.Get("/timeline"), getIndex)
	mux.HandleFunc(pat.Get("/categories/:category_id/items"), getIndex)
	mux.HandleFunc(pat.Get("/sell"), getIndex)
	mux.HandleFunc(pat.Get("/items/:item_id"), getIndex)
	mux.HandleFunc(pat.Get("/items/:item_id/edit"), getIndex)
	mux.HandleFunc(pat.Get("/items/:item_id/buy"), getIndex)
	mux.HandleFunc(pat.Get("/buy/complete"), getIndex)
	mux.HandleFunc(pat.Get("/transactions/:transaction_id"), getIndex)
	mux.HandleFunc(pat.Get("/users/:user_id"), getIndex)
	mux.HandleFunc(pat.Get("/users/setting"), getIndex)
	// Assets
	mux.Handle(pat.Get("/*"), http.FileServer(http.Dir("../public")))
	log.Fatal(http.ListenAndServe(":8000", mux))
}

func getSession(r *http.Request) *sessions.Session {
	session, _ := store.Get(r, sessionName)

	return session
}

func getCSRFToken(r *http.Request) string {
	session := getSession(r)

	csrfToken, ok := session.Values["csrf_token"]
	if !ok {
		return ""
	}

	return csrfToken.(string)
}

var userMap sync.Map

func getUserWithCache(q sqlx.Queryer, userID int64) (User, error) {
	cached, ok := userMap.Load(userID)
	if ok {
		return cached.(User), nil
	}

	user := User{}
	err := sqlx.Get(q, &user, "SELECT * FROM `users` WHERE `id` = ?", userID)
	if err == nil {
		userMap.Store(userID, user)
	}
	return user, err
}

func updateUserWithCache(user User, userID int64) {
	userMap.Store(userID, user)
}

func initUsers() {
	users := []User{}

	dbx.Select(&users, "SELECT * FROM users")
	for _, user := range users {
		userMap.Store(user.ID, user)
	}
}

func getUser(r *http.Request) (user User, errCode int, errMsg string) {
	session := getSession(r)
	userID, ok := session.Values["user_id"]
	if !ok {
		return user, http.StatusNotFound, "no session"
	}

	var err error
	user, err = getUserWithCache(dbx, userID.(int64))
	if err == sql.ErrNoRows {
		return user, http.StatusNotFound, "user not found"
	}
	if err != nil {
		log.Print(err)
		return user, http.StatusInternalServerError, "db error"
	}

	return user, http.StatusOK, ""
}

func getUserSimpleByID(q sqlx.Queryer, userID int64) (userSimple UserSimple, err error) {
	user := User{}
	user, err = getUserWithCache(q, userID)
	if err != nil {
		return userSimple, err
	}
	userSimple.ID = user.ID
	userSimple.AccountName = user.AccountName
	userSimple.NumSellItems = user.NumSellItems
	return userSimple, err
}

var Categories []Category

func initCategories() error {
	err := dbx.Select(&Categories, "SELECT * FROM `categories`")
	for i, category := range Categories {
		if category.ParentID != 0 {
			parentCategory, err := getCategoryByID(dbx, category.ParentID)
			if err != nil {
				return err
			}
			category.ParentCategoryName = parentCategory.CategoryName
		}
		Categories[i] = category
	}
	return err
}

func getCategoryFromArr(categoryID int) Category {
	c := Category{}
	for _, category := range Categories {
		if category.ID == categoryID {
			c.ID = category.ID
			c.ParentID = category.ParentID
			c.CategoryName = category.CategoryName
			c.ParentCategoryName = category.ParentCategoryName
		}
	}
	return c
}

func getCategoryByID(q sqlx.Queryer, categoryID int) (category Category, err error) {
	err = sqlx.Get(q, &category, "SELECT * FROM `categories` WHERE `id` = ?", categoryID)
	if category.ParentID != 0 {
		parentCategory, err := getCategoryByID(q, category.ParentID)
		if err != nil {
			return category, err
		}
		category.ParentCategoryName = parentCategory.CategoryName
	}
	return category, err
}

var serviceUrlMap sync.Map

func getConfigByNameWithCache(name string) (string, bool) {
	cached, ok := serviceUrlMap.Load(name)
	if ok {
		return cached.(string), ok
	}
	return "", ok
}

func updateConfigWithCache(name, url string) {
	userMap.Store(name, url)
}

func getPaymentServiceURL() string {
	val, _ := getConfigByNameWithCache("payment_service_url")
	if val == "" {
		return DefaultPaymentServiceURL
	}
	return val
}

func getShipmentServiceURL() string {
	val, _ := getConfigByNameWithCache("shipment_service_url")
	if val == "" {
		return DefaultShipmentServiceURL
	}
	return val
}

func getIndex(w http.ResponseWriter, r *http.Request) {
	templates.ExecuteTemplate(w, "index.html", struct{}{})
}

func postInitialize(w http.ResponseWriter, r *http.Request) {
	ri := reqInitialize{}

	err := json.NewDecoder(r.Body).Decode(&ri)
	if err != nil {
		outputErrorMsg(w, http.StatusBadRequest, "json decode error")
		return
	}

	cmd := exec.Command("../sql/init.sh")
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stderr
	cmd.Run()
	if err != nil {
		outputErrorMsg(w, http.StatusInternalServerError, "exec init.sh error")
		return
	}

	updateConfigWithCache("payment_service_url", ri.PaymentServiceURL)
	updateConfigWithCache("shipment_service_url", ri.ShipmentServiceURL)

	initUsers()
	err = initCategories()
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "category init error")
		return
	}

	res := resInitialize{
		// キャンペーン実施時には還元率の設定を返す。詳しくはマニュアルを参照のこと。
		Campaign: 0,
		// 実装言語を返す
		Language: "Go",
	}

	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	json.NewEncoder(w).Encode(res)
}

func getNewItems(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	itemIDStr := query.Get("item_id")
	var itemID int64
	var err error
	if itemIDStr != "" {
		itemID, err = strconv.ParseInt(itemIDStr, 10, 64)
		if err != nil || itemID <= 0 {
			outputErrorMsg(w, http.StatusBadRequest, "item_id param error")
			return
		}
	}

	createdAtStr := query.Get("created_at")
	var createdAt int64
	if createdAtStr != "" {
		createdAt, err = strconv.ParseInt(createdAtStr, 10, 64)
		if err != nil || createdAt <= 0 {
			outputErrorMsg(w, http.StatusBadRequest, "created_at param error")
			return
		}
	}

	items := []Item{}
	if itemID > 0 && createdAt > 0 {
		// paging
		err := dbx.Select(&items,
			"SELECT * FROM `items` WHERE `status` IN (?,?) AND (`created_at` < ?  OR (`created_at` <= ? AND `id` < ?)) ORDER BY `created_at` DESC, `id` DESC LIMIT ?",
			ItemStatusOnSale,
			ItemStatusSoldOut,
			time.Unix(createdAt, 0),
			time.Unix(createdAt, 0),
			itemID,
			ItemsPerPage+1,
		)
		if err != nil {
			log.Print(err)
			outputErrorMsg(w, http.StatusInternalServerError, "db error")
			return
		}
	} else {
		// 1st page
		err := dbx.Select(&items,
			"SELECT * FROM `items` WHERE `status` IN (?,?) ORDER BY `created_at` DESC, `id` DESC LIMIT ?",
			ItemStatusOnSale,
			ItemStatusSoldOut,
			ItemsPerPage+1,
		)
		if err != nil {
			log.Print(err)
			outputErrorMsg(w, http.StatusInternalServerError, "db error")
			return
		}
	}

	itemSimples := []ItemSimple{}
	for _, item := range items {
		seller, err := getUserSimpleByID(dbx, item.SellerID)
		if err != nil {
			outputErrorMsg(w, http.StatusNotFound, "seller not found")
			return
		}
		category := getCategoryFromArr(item.CategoryID)
		itemSimples = append(itemSimples, ItemSimple{
			ID:         item.ID,
			SellerID:   item.SellerID,
			Seller:     &seller,
			Status:     item.Status,
			Name:       item.Name,
			Price:      item.Price,
			ImageURL:   getImageURL(item.ImageName),
			CategoryID: item.CategoryID,
			Category:   &category,
			CreatedAt:  item.CreatedAt.Unix(),
		})
	}

	hasNext := false
	if len(itemSimples) > ItemsPerPage {
		hasNext = true
		itemSimples = itemSimples[0:ItemsPerPage]
	}

	rni := resNewItems{
		Items:   itemSimples,
		HasNext: hasNext,
	}

	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	json.NewEncoder(w).Encode(rni)
}

func getNewCategoryItems(w http.ResponseWriter, r *http.Request) {
	rootCategoryIDStr := pat.Param(r, "root_category_id")
	rootCategoryID, err := strconv.Atoi(rootCategoryIDStr)
	if err != nil || rootCategoryID <= 0 {
		outputErrorMsg(w, http.StatusBadRequest, "incorrect category id")
		return
	}

	rootCategory := getCategoryFromArr(rootCategoryID)
	if rootCategory.ParentID != 0 {
		outputErrorMsg(w, http.StatusNotFound, "category not found")
		return
	}

	var categoryIDs []int
	err = dbx.Select(&categoryIDs, "SELECT id FROM `categories` WHERE parent_id=?", rootCategory.ID)
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		return
	}

	query := r.URL.Query()
	itemIDStr := query.Get("item_id")
	var itemID int64
	if itemIDStr != "" {
		itemID, err = strconv.ParseInt(itemIDStr, 10, 64)
		if err != nil || itemID <= 0 {
			outputErrorMsg(w, http.StatusBadRequest, "item_id param error")
			return
		}
	}

	createdAtStr := query.Get("created_at")
	var createdAt int64
	if createdAtStr != "" {
		createdAt, err = strconv.ParseInt(createdAtStr, 10, 64)
		if err != nil || createdAt <= 0 {
			outputErrorMsg(w, http.StatusBadRequest, "created_at param error")
			return
		}
	}

	var inQuery string
	var inArgs []interface{}
	if itemID > 0 && createdAt > 0 {
		// paging
		inQuery, inArgs, err = sqlx.In(
			"SELECT * FROM `items` WHERE `status` IN (?,?) AND category_id IN (?) AND (`created_at` < ?  OR (`created_at` <= ? AND `id` < ?)) ORDER BY `created_at` DESC, `id` DESC LIMIT ?",
			ItemStatusOnSale,
			ItemStatusSoldOut,
			categoryIDs,
			time.Unix(createdAt, 0),
			time.Unix(createdAt, 0),
			itemID,
			ItemsPerPage+1,
		)
		if err != nil {
			log.Print(err)
			outputErrorMsg(w, http.StatusInternalServerError, "db error")
			return
		}
	} else {
		// 1st page
		inQuery, inArgs, err = sqlx.In(
			"SELECT * FROM `items` WHERE `status` IN (?,?) AND category_id IN (?) ORDER BY created_at DESC, id DESC LIMIT ?",
			ItemStatusOnSale,
			ItemStatusSoldOut,
			categoryIDs,
			ItemsPerPage+1,
		)
		if err != nil {
			log.Print(err)
			outputErrorMsg(w, http.StatusInternalServerError, "db error")
			return
		}
	}

	items := []Item{}
	err = dbx.Select(&items, inQuery, inArgs...)

	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		return
	}

	itemSimples := []ItemSimple{}
	for _, item := range items {
		seller, err := getUserSimpleByID(dbx, item.SellerID)
		if err != nil {
			outputErrorMsg(w, http.StatusNotFound, "seller not found")
			return
		}
		category := getCategoryFromArr(item.CategoryID)
		itemSimples = append(itemSimples, ItemSimple{
			ID:         item.ID,
			SellerID:   item.SellerID,
			Seller:     &seller,
			Status:     item.Status,
			Name:       item.Name,
			Price:      item.Price,
			ImageURL:   getImageURL(item.ImageName),
			CategoryID: item.CategoryID,
			Category:   &category,
			CreatedAt:  item.CreatedAt.Unix(),
		})
	}

	hasNext := false
	if len(itemSimples) > ItemsPerPage {
		hasNext = true
		itemSimples = itemSimples[0:ItemsPerPage]
	}

	rni := resNewItems{
		RootCategoryID:   rootCategory.ID,
		RootCategoryName: rootCategory.CategoryName,
		Items:            itemSimples,
		HasNext:          hasNext,
	}

	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	json.NewEncoder(w).Encode(rni)

}

func getUserItems(w http.ResponseWriter, r *http.Request) {
	userIDStr := pat.Param(r, "user_id")
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil || userID <= 0 {
		outputErrorMsg(w, http.StatusBadRequest, "incorrect user id")
		return
	}

	userSimple, err := getUserSimpleByID(dbx, userID)
	if err != nil {
		outputErrorMsg(w, http.StatusNotFound, "user not found")
		return
	}

	query := r.URL.Query()
	itemIDStr := query.Get("item_id")
	var itemID int64
	if itemIDStr != "" {
		itemID, err = strconv.ParseInt(itemIDStr, 10, 64)
		if err != nil || itemID <= 0 {
			outputErrorMsg(w, http.StatusBadRequest, "item_id param error")
			return
		}
	}

	createdAtStr := query.Get("created_at")
	var createdAt int64
	if createdAtStr != "" {
		createdAt, err = strconv.ParseInt(createdAtStr, 10, 64)
		if err != nil || createdAt <= 0 {
			outputErrorMsg(w, http.StatusBadRequest, "created_at param error")
			return
		}
	}

	items := []Item{}
	if itemID > 0 && createdAt > 0 {
		// paging
		err := dbx.Select(&items,
			"SELECT * FROM `items` WHERE `seller_id` = ? AND `status` IN (?,?,?) AND (`created_at` < ?  OR (`created_at` <= ? AND `id` < ?)) ORDER BY `created_at` DESC, `id` DESC LIMIT ?",
			userSimple.ID,
			ItemStatusOnSale,
			ItemStatusTrading,
			ItemStatusSoldOut,
			time.Unix(createdAt, 0),
			time.Unix(createdAt, 0),
			itemID,
			ItemsPerPage+1,
		)
		if err != nil {
			log.Print(err)
			outputErrorMsg(w, http.StatusInternalServerError, "db error")
			return
		}
	} else {
		// 1st page
		err := dbx.Select(&items,
			"SELECT * FROM `items` WHERE `seller_id` = ? AND `status` IN (?,?,?) ORDER BY `created_at` DESC, `id` DESC LIMIT ?",
			userSimple.ID,
			ItemStatusOnSale,
			ItemStatusTrading,
			ItemStatusSoldOut,
			ItemsPerPage+1,
		)
		if err != nil {
			log.Print(err)
			outputErrorMsg(w, http.StatusInternalServerError, "db error")
			return
		}
	}

	itemSimples := []ItemSimple{}
	for _, item := range items {
		category := getCategoryFromArr(item.CategoryID)
		itemSimples = append(itemSimples, ItemSimple{
			ID:         item.ID,
			SellerID:   item.SellerID,
			Seller:     &userSimple,
			Status:     item.Status,
			Name:       item.Name,
			Price:      item.Price,
			ImageURL:   getImageURL(item.ImageName),
			CategoryID: item.CategoryID,
			Category:   &category,
			CreatedAt:  item.CreatedAt.Unix(),
		})
	}

	hasNext := false
	if len(itemSimples) > ItemsPerPage {
		hasNext = true
		itemSimples = itemSimples[0:ItemsPerPage]
	}

	rui := resUserItems{
		User:    &userSimple,
		Items:   itemSimples,
		HasNext: hasNext,
	}

	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	json.NewEncoder(w).Encode(rui)
}

func getTransactions(w http.ResponseWriter, r *http.Request) {

	user, errCode, errMsg := getUser(r)
	if errMsg != "" {
		outputErrorMsg(w, errCode, errMsg)
		return
	}

	query := r.URL.Query()
	itemIDStr := query.Get("item_id")
	var err error
	var itemID int64
	if itemIDStr != "" {
		itemID, err = strconv.ParseInt(itemIDStr, 10, 64)
		if err != nil || itemID <= 0 {
			outputErrorMsg(w, http.StatusBadRequest, "item_id param error")
			return
		}
	}

	createdAtStr := query.Get("created_at")
	var createdAt int64
	if createdAtStr != "" {
		createdAt, err = strconv.ParseInt(createdAtStr, 10, 64)
		if err != nil || createdAt <= 0 {
			outputErrorMsg(w, http.StatusBadRequest, "created_at param error")
			return
		}
	}

	var rows *sqlx.Rows
	if itemID > 0 && createdAt > 0 {
		// paging
		query := `
			(SELECT 
				i.id, 
				i.seller_id, 
				i.status, 
				i.name, 
				i.price,
				i.description, 
				i.image_name, 
				i.category_id,
				i.created_at, 
				i.buyer_id, 
				ship.id as ship_id, 
				ship.reserve_id,
				ship.status as ship_status
			FROM items AS i
			LEFT JOIN (
				SELECT 
					t.id, t.status, t.item_id, s.reserve_id 
				FROM transaction_evidences AS t 
				INNER JOIN shippings AS s 
				ON t.id = s.transaction_evidence_id 
			) AS ship ON i.id = ship.item_id
			WHERE i.seller_id = ?
				AND (i.created_at < ? OR (i.created_at <= ? AND i.id < ?)))
			UNION
			(SELECT 
				i.id, 
				i.seller_id, 
				i.status, 
				i.name, 
				i.price,
				i.description, 
				i.image_name, 
				i.category_id,
				i.created_at, 
				i.buyer_id, 
				ship.id as ship_id, 
				ship.reserve_id,
				ship.status as ship_status
			FROM items AS i
			LEFT JOIN (
				SELECT 
					t.id, t.status, t.item_id, s.reserve_id 
				FROM transaction_evidences AS t 
				INNER JOIN shippings AS s 
				ON t.id = s.transaction_evidence_id 
			) AS ship ON i.id = ship.item_id
			WHERE i.buyer_id = ?
				AND (i.created_at < ? OR (i.created_at <= ? AND i.id < ?)))
			ORDER BY created_at DESC, id DESC LIMIT ?
			`
		rows, err = dbx.Queryx(query,
			user.ID,
			time.Unix(createdAt, 0),
			time.Unix(createdAt, 0),
			itemID,
			user.ID,
			time.Unix(createdAt, 0),
			time.Unix(createdAt, 0),
			itemID,
			TransactionsPerPage+1,
		)
		if err != nil {
			log.Print(err)
			outputErrorMsg(w, http.StatusInternalServerError, "db error")
			return
		}
	} else {
		// 1st page
		query := `
			(SELECT
				i.id, 
				i.seller_id, 
				i.status, 
				i.name, 
				i.price,
				i.description, 
				i.image_name, 
				i.category_id,
				i.created_at, 
				i.buyer_id, 
				ship.id as ship_id, 
				ship.reserve_id,
				ship.status as ship_status
			FROM items as i
			LEFT JOIN (
				SELECT 
					t.id, t.status, t.item_id, s.reserve_id
				FROM transaction_evidences AS t
				INNER JOIN shippings AS s
				ON t.id = s.transaction_evidence_id
			) AS ship ON i.id = ship.item_id
			WHERE i.seller_id = ?)
			UNION
			(SELECT
				i.id, 
				i.seller_id, 
				i.status, 
				i.name, 
				i.price,
				i.description, 
				i.image_name, 
				i.category_id,
				i.created_at, 
				i.buyer_id, 
				ship.id as ship_id, 
				ship.reserve_id,
				ship.status as ship_status
			FROM items as i
			LEFT JOIN (
				SELECT 
					t.id, t.status, t.item_id, s.reserve_id
				FROM transaction_evidences AS t
				INNER JOIN shippings AS s
				ON t.id = s.transaction_evidence_id
			) AS ship ON i.id = ship.item_id
			WHERE i.buyer_id = ?)
			ORDER BY created_at DESC, id DESC LIMIT ?
			`
		rows, err = dbx.Queryx(query,
			user.ID,
			user.ID,
			TransactionsPerPage+1,
		)
		if err != nil {
			log.Print(err)
			outputErrorMsg(w, http.StatusInternalServerError, "db error")
			return
		}
	}

	var wait sync.WaitGroup
	itemDetails := []*ItemDetail{}
	for rows.Next() {
		item := Item{}
		shipID := sql.NullInt64{}
		reserveID := sql.NullString{}
		shipStatus := sql.NullString{}
		err := rows.Scan(
			&item.ID,
			&item.SellerID,
			&item.Status,
			&item.Name,
			&item.Price,
			&item.Description,
			&item.ImageName,
			&item.CategoryID,
			&item.CreatedAt,
			&item.BuyerID,
			&shipID,
			&reserveID,
			&shipStatus,
		)

		if err != nil {
			log.Print(err)
			outputErrorMsg(w, http.StatusInternalServerError, "db error")
			return
		}

		seller, err := getUserSimpleByID(dbx, item.SellerID)
		if err != nil {
			outputErrorMsg(w, http.StatusNotFound, "seller not found")
			return
		}
		category := getCategoryFromArr(item.CategoryID)
		itemDetail := &ItemDetail{
			ID:       item.ID,
			SellerID: item.SellerID,
			Seller:   &seller,
			// BuyerID
			// Buyer
			Status:      item.Status,
			Name:        item.Name,
			Price:       item.Price,
			Description: item.Description,
			ImageURL:    getImageURL(item.ImageName),
			CategoryID:  item.CategoryID,
			// TransactionEvidenceID
			// TransactionEvidenceStatus
			// ShippingStatus
			Category:  &category,
			CreatedAt: item.CreatedAt.Unix(),
		}

		if item.BuyerID != 0 {
			buyer, err := getUserSimpleByID(dbx, item.BuyerID)
			if err != nil {
				outputErrorMsg(w, http.StatusNotFound, "buyer not found")
				return
			}
			itemDetail.BuyerID = item.BuyerID
			itemDetail.Buyer = &buyer
		}

		if shipID.Valid && reserveID.Valid {
			shipIDValue := shipID.Int64
			shipStatusValue := shipStatus.String
			reserveIDValue := reserveID.String
			itemDetail.TransactionEvidenceID = shipIDValue
			itemDetail.TransactionEvidenceStatus = shipStatusValue

			switch itemDetail.Status {
			case "on_sale":
			case "sold_out":
				itemDetail.ShippingStatus = "done"
			default:
				{
					wait.Add(1)
					go func() {
						ssr, err := APIShipmentStatus(getShipmentServiceURL(), &APIShipmentStatusReq{
							ReserveID: reserveIDValue,
						})
						if err != nil {
							log.Print(err)
							outputErrorMsg(w, http.StatusInternalServerError, "failed to request to shipment service")
							return
						}
						itemDetail.ShippingStatus = ssr.Status
						wait.Done()
					}()
				}
			}
		}
		itemDetails = append(itemDetails, itemDetail)
	}
	wait.Wait()

	hasNext := false
	if len(itemDetails) > TransactionsPerPage {
		hasNext = true
		itemDetails = itemDetails[0:TransactionsPerPage]
	}

	// log.Print("sellerID: ", user.ID)
	// for i, item := range itemDetails {
	// 	log.Print(fmt.Sprintf("%d: ID: %d, Price: %d, Status: %s, shippingStatus: %s\n", i+1, item.ID, item.Price, item.Status, item.ShippingStatus))
	// }

	rts := resTransactions{
		Items:   itemDetails,
		HasNext: hasNext,
	}

	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	json.NewEncoder(w).Encode(rts)

}

func getItem(w http.ResponseWriter, r *http.Request) {
	itemIDStr := pat.Param(r, "item_id")
	itemID, err := strconv.ParseInt(itemIDStr, 10, 64)
	if err != nil || itemID <= 0 {
		outputErrorMsg(w, http.StatusBadRequest, "incorrect item id")
		return
	}

	user, errCode, errMsg := getUser(r)
	if errMsg != "" {
		outputErrorMsg(w, errCode, errMsg)
		return
	}

	item := Item{}
	err = dbx.Get(&item, "SELECT * FROM `items` WHERE `id` = ?", itemID)
	if err == sql.ErrNoRows {
		outputErrorMsg(w, http.StatusNotFound, "item not found")
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		return
	}

	category := getCategoryFromArr(item.CategoryID)

	seller, err := getUserSimpleByID(dbx, item.SellerID)
	if err != nil {
		outputErrorMsg(w, http.StatusNotFound, "seller not found")
		return
	}

	itemDetail := ItemDetail{
		ID:       item.ID,
		SellerID: item.SellerID,
		Seller:   &seller,
		// BuyerID
		// Buyer
		Status:      item.Status,
		Name:        item.Name,
		Price:       item.Price,
		Description: item.Description,
		ImageURL:    getImageURL(item.ImageName),
		CategoryID:  item.CategoryID,
		// TransactionEvidenceID
		// TransactionEvidenceStatus
		// ShippingStatus
		Category:  &category,
		CreatedAt: item.CreatedAt.Unix(),
	}

	if (user.ID == item.SellerID || user.ID == item.BuyerID) && item.BuyerID != 0 {
		buyer, err := getUserSimpleByID(dbx, item.BuyerID)
		if err != nil {
			outputErrorMsg(w, http.StatusNotFound, "buyer not found")
			return
		}
		itemDetail.BuyerID = item.BuyerID
		itemDetail.Buyer = &buyer

		transactionEvidence := TransactionEvidence{}
		err = dbx.Get(&transactionEvidence, "SELECT * FROM `transaction_evidences` WHERE `item_id` = ?", item.ID)
		if err != nil && err != sql.ErrNoRows {
			// It's able to ignore ErrNoRows
			log.Print(err)
			outputErrorMsg(w, http.StatusInternalServerError, "db error")
			return
		}

		if transactionEvidence.ID > 0 {
			shipping := Shipping{}
			err = dbx.Get(&shipping, "SELECT * FROM `shippings` WHERE `transaction_evidence_id` = ?", transactionEvidence.ID)
			if err == sql.ErrNoRows {
				outputErrorMsg(w, http.StatusNotFound, "shipping not found")
				return
			}
			if err != nil {
				log.Print(err)
				outputErrorMsg(w, http.StatusInternalServerError, "db error")
				return
			}

			itemDetail.TransactionEvidenceID = transactionEvidence.ID
			itemDetail.TransactionEvidenceStatus = transactionEvidence.Status
			itemDetail.ShippingStatus = shipping.Status
		}
	}

	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	json.NewEncoder(w).Encode(itemDetail)
}

func postItemEdit(w http.ResponseWriter, r *http.Request) {
	rie := reqItemEdit{}
	err := json.NewDecoder(r.Body).Decode(&rie)
	if err != nil {
		outputErrorMsg(w, http.StatusBadRequest, "json decode error")
		return
	}

	csrfToken := rie.CSRFToken
	itemID := rie.ItemID
	price := rie.ItemPrice

	if csrfToken != getCSRFToken(r) {
		outputErrorMsg(w, http.StatusUnprocessableEntity, "csrf token error")

		return
	}

	if price < ItemMinPrice || price > ItemMaxPrice {
		outputErrorMsg(w, http.StatusBadRequest, ItemPriceErrMsg)
		return
	}

	seller, errCode, errMsg := getUser(r)
	if errMsg != "" {
		outputErrorMsg(w, errCode, errMsg)
		return
	}

	targetItem := Item{}
	err = dbx.Get(&targetItem, "SELECT * FROM `items` WHERE `id` = ?", itemID)
	if err == sql.ErrNoRows {
		outputErrorMsg(w, http.StatusNotFound, "item not found")
		return
	}
	if err != nil {
		log.Print(err)

		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		return
	}

	if targetItem.SellerID != seller.ID {
		outputErrorMsg(w, http.StatusForbidden, "自分の商品以外は編集できません")
		return
	}

	tx := dbx.MustBegin()
	err = tx.Get(&targetItem, "SELECT * FROM `items` WHERE `id` = ? FOR UPDATE", itemID)
	if err != nil {
		log.Print(err)

		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	if targetItem.Status != ItemStatusOnSale {
		outputErrorMsg(w, http.StatusForbidden, "販売中の商品以外編集できません")
		tx.Rollback()
		return
	}

	_, err = tx.Exec("UPDATE `items` SET `price` = ?, `updated_at` = ? WHERE `id` = ?",
		price,
		time.Now(),
		itemID,
	)
	if err != nil {
		log.Print(err)

		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	err = tx.Get(&targetItem, "SELECT * FROM `items` WHERE `id` = ?", itemID)
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	tx.Commit()

	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	json.NewEncoder(w).Encode(&resItemEdit{
		ItemID:        targetItem.ID,
		ItemPrice:     targetItem.Price,
		ItemCreatedAt: targetItem.CreatedAt.Unix(),
		ItemUpdatedAt: targetItem.UpdatedAt.Unix(),
	})
}

func getQRCode(w http.ResponseWriter, r *http.Request) {
	transactionEvidenceIDStr := pat.Param(r, "transaction_evidence_id")
	transactionEvidenceID, err := strconv.ParseInt(transactionEvidenceIDStr, 10, 64)
	if err != nil || transactionEvidenceID <= 0 {
		outputErrorMsg(w, http.StatusBadRequest, "incorrect transaction_evidence id")
		return
	}

	seller, errCode, errMsg := getUser(r)
	if errMsg != "" {
		outputErrorMsg(w, errCode, errMsg)
		return
	}

	transactionEvidence := TransactionEvidence{}
	err = dbx.Get(&transactionEvidence, "SELECT * FROM `transaction_evidences` WHERE `id` = ?", transactionEvidenceID)
	if err == sql.ErrNoRows {
		outputErrorMsg(w, http.StatusNotFound, "transaction_evidences not found")
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		return
	}

	if transactionEvidence.SellerID != seller.ID {
		outputErrorMsg(w, http.StatusForbidden, "権限がありません")
		return
	}

	shipping := Shipping{}
	err = dbx.Get(&shipping, "SELECT * FROM `shippings` WHERE `transaction_evidence_id` = ?", transactionEvidence.ID)
	if err == sql.ErrNoRows {
		outputErrorMsg(w, http.StatusNotFound, "shippings not found")
		return
	}
	if err != nil {
		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		return
	}

	if shipping.Status != ShippingsStatusWaitPickup && shipping.Status != ShippingsStatusShipping {
		outputErrorMsg(w, http.StatusForbidden, "qrcode not available")
		return
	}

	if len(shipping.ImgBinary) == 0 {
		outputErrorMsg(w, http.StatusInternalServerError, "empty qrcode image")
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Write(shipping.ImgBinary)
}

func postBuy(w http.ResponseWriter, r *http.Request) {
	rb := reqBuy{}

	err := json.NewDecoder(r.Body).Decode(&rb)
	if err != nil {
		outputErrorMsg(w, http.StatusBadRequest, "json decode error")
		return
	}

	if rb.CSRFToken != getCSRFToken(r) {
		outputErrorMsg(w, http.StatusUnprocessableEntity, "csrf token error")

		return
	}

	buyer, errCode, errMsg := getUser(r)
	if errMsg != "" {
		outputErrorMsg(w, errCode, errMsg)
		return
	}

	tx := dbx.MustBegin()

	// itemを取得
	targetItem := Item{}
	err = tx.Get(&targetItem, "SELECT * FROM `items` WHERE `id` = ? FOR UPDATE", rb.ItemID)
	if err == sql.ErrNoRows {
		outputErrorMsg(w, http.StatusNotFound, "item not found")
		tx.Rollback()
		return
	}
	if err != nil {
		log.Print(err)

		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	if targetItem.Status != ItemStatusOnSale {
		outputErrorMsg(w, http.StatusForbidden, "item is not for sale")
		tx.Rollback()
		return
	}

	if targetItem.SellerID == buyer.ID {
		outputErrorMsg(w, http.StatusForbidden, "自分の商品は買えません")
		tx.Rollback()
		return
	}

	// sellerの情報を取得
	seller := User{}
	seller, err = getUserWithCache(tx, targetItem.SellerID)
	if err == sql.ErrNoRows {
		outputErrorMsg(w, http.StatusNotFound, "seller not found")
		tx.Rollback()
		return
	}
	if err != nil {
		log.Print(err)

		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	category := getCategoryFromArr(targetItem.CategoryID)

	// 取引履歴を挿入
	result, err := tx.Exec("INSERT INTO `transaction_evidences` (`seller_id`, `buyer_id`, `status`, `item_id`, `item_name`, `item_price`, `item_description`,`item_category_id`,`item_root_category_id`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		targetItem.SellerID,
		buyer.ID,
		TransactionEvidenceStatusWaitShipping,
		targetItem.ID,
		targetItem.Name,
		targetItem.Price,
		targetItem.Description,
		category.ID,
		category.ParentID,
	)
	if err != nil {
		log.Print(err)

		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	transactionEvidenceID, err := result.LastInsertId()
	if err != nil {
		log.Print(err)

		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	// 購入者情報をセット
	_, err = tx.Exec("UPDATE `items` SET `buyer_id` = ?, `status` = ?, `updated_at` = ? WHERE `id` = ?",
		buyer.ID,
		ItemStatusTrading,
		time.Now(),
		targetItem.ID,
	)
	if err != nil {
		log.Print(err)

		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	var wait sync.WaitGroup

	wait.Add(1)
	var scr *APIShipmentCreateRes
	var scErr error
	go func() {
		scr, err = APIShipmentCreate(getShipmentServiceURL(), &APIShipmentCreateReq{
			ToAddress:   buyer.Address,
			ToName:      buyer.AccountName,
			FromAddress: seller.Address,
			FromName:    seller.AccountName,
		})
		wait.Done()
	}()

	wait.Add(1)
	var pstr *APIPaymentServiceTokenRes
	var psErr error
	go func() {
		pstr, psErr = APIPaymentToken(getPaymentServiceURL(), &APIPaymentServiceTokenReq{
			ShopID: PaymentServiceIsucariShopID,
			Token:  rb.Token,
			APIKey: PaymentServiceIsucariAPIKey,
			Price:  targetItem.Price,
		})
		wait.Done()
	}()
	wait.Wait()

	if scErr != nil {
		log.Print(scErr)
		outputErrorMsg(w, http.StatusInternalServerError, "failed to request to shipment service")
		tx.Rollback()

		return
	}

	if psErr != nil {
		log.Print(psErr)

		outputErrorMsg(w, http.StatusInternalServerError, "payment service is failed")
		tx.Rollback()
		return
	}

	if pstr.Status == "invalid" {
		outputErrorMsg(w, http.StatusBadRequest, "カード情報に誤りがあります")
		tx.Rollback()
		return
	}

	if pstr.Status == "fail" {
		outputErrorMsg(w, http.StatusBadRequest, "カードの残高が足りません")
		tx.Rollback()
		return
	}

	if pstr.Status != "ok" {
		outputErrorMsg(w, http.StatusBadRequest, "想定外のエラー")
		tx.Rollback()
		return
	}

	// 決済が完了したので 輸送済みに情報追加
	_, err = tx.Exec("INSERT INTO `shippings` (`transaction_evidence_id`, `status`, `item_name`, `item_id`, `reserve_id`, `reserve_time`, `to_address`, `to_name`, `from_address`, `from_name`, `img_binary`) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
		transactionEvidenceID,
		ShippingsStatusInitial,
		targetItem.Name,
		targetItem.ID,
		scr.ReserveID,
		scr.ReserveTime,
		buyer.Address,
		buyer.AccountName,
		seller.Address,
		seller.AccountName,
		"",
	)
	if err != nil {
		log.Print(err)

		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	tx.Commit()

	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	json.NewEncoder(w).Encode(resBuy{TransactionEvidenceID: transactionEvidenceID})
}

func postShip(w http.ResponseWriter, r *http.Request) {
	reqps := reqPostShip{}

	err := json.NewDecoder(r.Body).Decode(&reqps)
	if err != nil {
		outputErrorMsg(w, http.StatusBadRequest, "json decode error")
		return
	}

	csrfToken := reqps.CSRFToken
	itemID := reqps.ItemID

	if csrfToken != getCSRFToken(r) {
		outputErrorMsg(w, http.StatusUnprocessableEntity, "csrf token error")

		return
	}

	seller, errCode, errMsg := getUser(r)
	if errMsg != "" {
		outputErrorMsg(w, errCode, errMsg)
		return
	}

	transactionEvidence := TransactionEvidence{}
	err = dbx.Get(&transactionEvidence, "SELECT * FROM `transaction_evidences` WHERE `item_id` = ?", itemID)
	if err == sql.ErrNoRows {
		outputErrorMsg(w, http.StatusNotFound, "transaction_evidences not found")
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "db error")

		return
	}

	if transactionEvidence.SellerID != seller.ID {
		outputErrorMsg(w, http.StatusForbidden, "権限がありません")
		return
	}

	tx := dbx.MustBegin()

	shipping := Shipping{}
	err = tx.Get(&shipping, "SELECT * FROM `shippings` WHERE `transaction_evidence_id` = ? FOR UPDATE", transactionEvidence.ID)
	if err == sql.ErrNoRows {
		outputErrorMsg(w, http.StatusNotFound, "shippings not found")
		tx.Rollback()
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	var wait sync.WaitGroup
	wait.Add(1)
	var img []byte
	var imgErr error
	go func() {
		img, imgErr = APIShipmentRequest(getShipmentServiceURL(), &APIShipmentRequestReq{
			ReserveID: shipping.ReserveID,
		})
		wait.Done()
	}()

	item := Item{}
	err = tx.Get(&item, "SELECT * FROM `items` WHERE `id` = ? FOR UPDATE", itemID)
	if err == sql.ErrNoRows {
		outputErrorMsg(w, http.StatusNotFound, "item not found")
		tx.Rollback()
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	if item.Status != ItemStatusTrading {
		outputErrorMsg(w, http.StatusForbidden, "商品が取引中ではありません")
		tx.Rollback()
		return
	}

	err = tx.Get(&transactionEvidence, "SELECT * FROM `transaction_evidences` WHERE `id` = ? FOR UPDATE", transactionEvidence.ID)
	if err == sql.ErrNoRows {
		outputErrorMsg(w, http.StatusNotFound, "transaction_evidences not found")
		tx.Rollback()
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	if transactionEvidence.Status != TransactionEvidenceStatusWaitShipping {
		outputErrorMsg(w, http.StatusForbidden, "準備ができていません")
		tx.Rollback()
		return
	}

	wait.Wait()
	if imgErr != nil {
		log.Print(imgErr)
		outputErrorMsg(w, http.StatusInternalServerError, "failed to request to shipment service")
		tx.Rollback()

		return
	}

	_, err = tx.Exec("UPDATE `shippings` SET `status` = ?, `img_binary` = ?, `updated_at` = ? WHERE `transaction_evidence_id` = ?",
		ShippingsStatusWaitPickup,
		img,
		time.Now(),
		transactionEvidence.ID,
	)
	if err != nil {
		log.Print(err)

		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	tx.Commit()

	rps := resPostShip{
		Path:      fmt.Sprintf("/transactions/%d.png", transactionEvidence.ID),
		ReserveID: shipping.ReserveID,
	}
	json.NewEncoder(w).Encode(rps)
}

func postShipDone(w http.ResponseWriter, r *http.Request) {
	reqpsd := reqPostShipDone{}

	err := json.NewDecoder(r.Body).Decode(&reqpsd)
	if err != nil {
		outputErrorMsg(w, http.StatusBadRequest, "json decode error")
		return
	}

	csrfToken := reqpsd.CSRFToken
	itemID := reqpsd.ItemID

	if csrfToken != getCSRFToken(r) {
		outputErrorMsg(w, http.StatusUnprocessableEntity, "csrf token error")

		return
	}

	seller, errCode, errMsg := getUser(r)
	if errMsg != "" {
		outputErrorMsg(w, errCode, errMsg)
		return
	}

	transactionEvidence := TransactionEvidence{}
	err = dbx.Get(&transactionEvidence, "SELECT id, seller_id FROM `transaction_evidences` WHERE `item_id` = ?", itemID)
	if err == sql.ErrNoRows {
		outputErrorMsg(w, http.StatusNotFound, "transaction_evidence not found")
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "db error")

		return
	}

	if transactionEvidence.SellerID != seller.ID {
		outputErrorMsg(w, http.StatusForbidden, "権限がありません")
		return
	}

	tx := dbx.MustBegin()

	item := Item{}
	err = tx.Get(&item, "SELECT * FROM `items` WHERE `id` = ? FOR UPDATE", itemID)
	if err == sql.ErrNoRows {
		outputErrorMsg(w, http.StatusNotFound, "items not found")
		tx.Rollback()
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	if item.Status != ItemStatusTrading {
		outputErrorMsg(w, http.StatusForbidden, "商品が取引中ではありません")
		tx.Rollback()
		return
	}

	err = tx.Get(&transactionEvidence, "SELECT id, status FROM `transaction_evidences` WHERE `id` = ? FOR UPDATE", transactionEvidence.ID)
	if err == sql.ErrNoRows {
		outputErrorMsg(w, http.StatusNotFound, "transaction_evidences not found")
		tx.Rollback()
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	if transactionEvidence.Status != TransactionEvidenceStatusWaitShipping {
		outputErrorMsg(w, http.StatusForbidden, "準備ができていません")
		tx.Rollback()
		return
	}

	shipping := Shipping{}
	err = tx.Get(&shipping, "SELECT reserve_id FROM `shippings` WHERE `transaction_evidence_id` = ? FOR UPDATE", transactionEvidence.ID)
	if err == sql.ErrNoRows {
		outputErrorMsg(w, http.StatusNotFound, "shippings not found")
		tx.Rollback()
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	var wait sync.WaitGroup
	wait.Add(1)
	var ssr *APIShipmentStatusRes
	var ssrErr error
	go func() {
		ssr, ssrErr = APIShipmentStatus(getShipmentServiceURL(), &APIShipmentStatusReq{
			ReserveID: shipping.ReserveID,
		})
		wait.Done()
	}()
	wait.Wait()

	if ssrErr != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "failed to request to shipment service")
		tx.Rollback()

		return
	}

	if !(ssr.Status == ShippingsStatusShipping || ssr.Status == ShippingsStatusDone) {
		outputErrorMsg(w, http.StatusForbidden, "shipment service側で配送中か配送完了になっていません")
		tx.Rollback()
		return
	}

	_, err = tx.Exec("UPDATE `shippings` SET `status` = ?, `updated_at` = ? WHERE `transaction_evidence_id` = ?",
		ssr.Status,
		time.Now(),
		transactionEvidence.ID,
	)
	if err != nil {
		log.Print(err)

		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	_, err = tx.Exec("UPDATE `transaction_evidences` SET `status` = ?, `updated_at` = ? WHERE `id` = ?",
		TransactionEvidenceStatusWaitDone,
		time.Now(),
		transactionEvidence.ID,
	)
	if err != nil {
		log.Print(err)

		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	tx.Commit()

	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	json.NewEncoder(w).Encode(resBuy{TransactionEvidenceID: transactionEvidence.ID})
}

func postComplete(w http.ResponseWriter, r *http.Request) {
	reqpc := reqPostComplete{}

	err := json.NewDecoder(r.Body).Decode(&reqpc)
	if err != nil {
		outputErrorMsg(w, http.StatusBadRequest, "json decode error")
		return
	}

	csrfToken := reqpc.CSRFToken
	itemID := reqpc.ItemID

	if csrfToken != getCSRFToken(r) {
		outputErrorMsg(w, http.StatusUnprocessableEntity, "csrf token error")

		return
	}

	buyer, errCode, errMsg := getUser(r)
	if errMsg != "" {
		outputErrorMsg(w, errCode, errMsg)
		return
	}

	transactionEvidence := TransactionEvidence{}
	err = dbx.Get(&transactionEvidence, "SELECT * FROM `transaction_evidences` WHERE `item_id` = ?", itemID)
	if err == sql.ErrNoRows {
		outputErrorMsg(w, http.StatusNotFound, "transaction_evidence not found")
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "db error")

		return
	}

	if transactionEvidence.BuyerID != buyer.ID {
		outputErrorMsg(w, http.StatusForbidden, "権限がありません")
		return
	}

	tx := dbx.MustBegin()
	item := Item{}
	err = tx.Get(&item, "SELECT * FROM `items` WHERE `id` = ? FOR UPDATE", itemID)
	if err == sql.ErrNoRows {
		outputErrorMsg(w, http.StatusNotFound, "items not found")
		tx.Rollback()
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	if item.Status != ItemStatusTrading {
		outputErrorMsg(w, http.StatusForbidden, "商品が取引中ではありません")
		tx.Rollback()
		return
	}

	err = tx.Get(&transactionEvidence, "SELECT * FROM `transaction_evidences` WHERE `item_id` = ? FOR UPDATE", itemID)
	if err == sql.ErrNoRows {
		outputErrorMsg(w, http.StatusNotFound, "transaction_evidences not found")
		tx.Rollback()
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	if transactionEvidence.Status != TransactionEvidenceStatusWaitDone {
		outputErrorMsg(w, http.StatusForbidden, "準備ができていません")
		tx.Rollback()
		return
	}

	shipping := Shipping{}
	err = tx.Get(&shipping, "SELECT * FROM `shippings` WHERE `transaction_evidence_id` = ? FOR UPDATE", transactionEvidence.ID)
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	var wait sync.WaitGroup
	wait.Add(1)
	var ssr *APIShipmentStatusRes
	var ssrErr error
	go func() {
		ssr, ssrErr = APIShipmentStatus(getShipmentServiceURL(), &APIShipmentStatusReq{
			ReserveID: shipping.ReserveID,
		})
		wait.Done()
	}()
	wait.Wait()

	if ssrErr != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "failed to request to shipment service")
		tx.Rollback()

		return
	}

	if !(ssr.Status == ShippingsStatusDone) {
		outputErrorMsg(w, http.StatusBadRequest, "shipment service側で配送完了になっていません")
		tx.Rollback()
		return
	}

	_, err = tx.Exec("UPDATE `shippings` SET `status` = ?, `updated_at` = ? WHERE `transaction_evidence_id` = ?",
		ShippingsStatusDone,
		time.Now(),
		transactionEvidence.ID,
	)
	if err != nil {
		log.Print(err)

		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	_, err = tx.Exec("UPDATE `transaction_evidences` SET `status` = ?, `updated_at` = ? WHERE `id` = ?",
		TransactionEvidenceStatusDone,
		time.Now(),
		transactionEvidence.ID,
	)
	if err != nil {
		log.Print(err)

		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	_, err = tx.Exec("UPDATE `items` SET `status` = ?, `updated_at` = ? WHERE `id` = ?",
		ItemStatusSoldOut,
		time.Now(),
		itemID,
	)
	if err != nil {
		log.Print(err)

		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	tx.Commit()

	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	json.NewEncoder(w).Encode(resBuy{TransactionEvidenceID: transactionEvidence.ID})
}

func postSell(w http.ResponseWriter, r *http.Request) {
	csrfToken := r.FormValue("csrf_token")
	name := r.FormValue("name")
	description := r.FormValue("description")
	priceStr := r.FormValue("price")
	categoryIDStr := r.FormValue("category_id")

	f, header, err := r.FormFile("image")
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusBadRequest, "image error")
		return
	}
	defer f.Close()

	if csrfToken != getCSRFToken(r) {
		outputErrorMsg(w, http.StatusUnprocessableEntity, "csrf token error")
		return
	}

	categoryID, err := strconv.Atoi(categoryIDStr)
	if err != nil || categoryID < 0 {
		outputErrorMsg(w, http.StatusBadRequest, "category id error")
		return
	}

	price, err := strconv.Atoi(priceStr)
	if err != nil {
		outputErrorMsg(w, http.StatusBadRequest, "price error")
		return
	}

	if name == "" || description == "" || price == 0 || categoryID == 0 {
		outputErrorMsg(w, http.StatusBadRequest, "all parameters are required")

		return
	}

	if price < ItemMinPrice || price > ItemMaxPrice {
		outputErrorMsg(w, http.StatusBadRequest, ItemPriceErrMsg)

		return
	}

	category := getCategoryFromArr(categoryID)
	if category.ParentID == 0 {
		log.Print(categoryID, category)
		outputErrorMsg(w, http.StatusBadRequest, "Incorrect category ID")
		return
	}

	user, errCode, errMsg := getUser(r)
	if errMsg != "" {
		outputErrorMsg(w, errCode, errMsg)
		return
	}

	img, err := ioutil.ReadAll(f)
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "image error")
		return
	}

	ext := filepath.Ext(header.Filename)

	if !(ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".gif") {
		outputErrorMsg(w, http.StatusBadRequest, "unsupported image format error")
		return
	}

	if ext == ".jpeg" {
		ext = ".jpg"
	}

	imgName := fmt.Sprintf("%s%s", secureRandomStr(16), ext)
	err = ioutil.WriteFile(fmt.Sprintf("../public/upload/%s", imgName), img, 0644)
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "Saving image failed")
		return
	}

	seller, err := getUserWithCache(dbx, user.ID)
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		return
	}

	result, err := dbx.Exec("INSERT INTO `items` (`seller_id`, `status`, `name`, `price`, `description`,`image_name`,`category_id`) VALUES (?, ?, ?, ?, ?, ?, ?)",
		seller.ID,
		ItemStatusOnSale,
		name,
		price,
		description,
		imgName,
		category.ID,
	)
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		return
	}

	itemID, err := result.LastInsertId()
	if err != nil {
		log.Print(err)

		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		return
	}

	now := time.Now()

	seller.NumSellItems++
	seller.LastBump = now
	updateUserWithCache(seller, seller.ID)

	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	json.NewEncoder(w).Encode(resSell{ID: itemID})
}

func secureRandomStr(b int) string {
	k := make([]byte, b)
	if _, err := crand.Read(k); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", k)
}

func postBump(w http.ResponseWriter, r *http.Request) {
	rb := reqBump{}
	err := json.NewDecoder(r.Body).Decode(&rb)
	if err != nil {
		outputErrorMsg(w, http.StatusBadRequest, "json decode error")
		return
	}

	csrfToken := rb.CSRFToken
	itemID := rb.ItemID

	if csrfToken != getCSRFToken(r) {
		outputErrorMsg(w, http.StatusUnprocessableEntity, "csrf token error")
		return
	}

	user, errCode, errMsg := getUser(r)
	if errMsg != "" {
		outputErrorMsg(w, errCode, errMsg)
		return
	}

	tx := dbx.MustBegin()

	targetItem := Item{}
	err = tx.Get(&targetItem, "SELECT * FROM `items` WHERE `id` = ? FOR UPDATE", itemID)
	if err == sql.ErrNoRows {
		outputErrorMsg(w, http.StatusNotFound, "item not found")
		tx.Rollback()
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	if targetItem.SellerID != user.ID {
		outputErrorMsg(w, http.StatusForbidden, "自分の商品以外は編集できません")
		tx.Rollback()
		return
	}

	seller := User{}
	seller, err = getUserWithCache(tx, user.ID)
	if err == sql.ErrNoRows {
		outputErrorMsg(w, http.StatusNotFound, "user not found")
		tx.Rollback()
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	now := time.Now()
	// last_bump + 3s > now
	if seller.LastBump.Add(BumpChargeSeconds).After(now) {
		outputErrorMsg(w, http.StatusForbidden, "Bump not allowed")
		tx.Rollback()
		return
	}

	_, err = tx.Exec("UPDATE `items` SET `created_at`=?, `updated_at`=? WHERE id=?",
		now,
		now,
		targetItem.ID,
	)
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		return
	}

	seller.LastBump = now
	updateUserWithCache(seller, seller.ID)

	err = tx.Get(&targetItem, "SELECT * FROM `items` WHERE `id` = ?", itemID)
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	tx.Commit()

	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	json.NewEncoder(w).Encode(&resItemEdit{
		ItemID:        targetItem.ID,
		ItemPrice:     targetItem.Price,
		ItemCreatedAt: targetItem.CreatedAt.Unix(),
		ItemUpdatedAt: targetItem.UpdatedAt.Unix(),
	})
}

func getSettings(w http.ResponseWriter, r *http.Request) {
	csrfToken := getCSRFToken(r)

	user, _, errMsg := getUser(r)

	ress := resSetting{}
	ress.CSRFToken = csrfToken
	if errMsg == "" {
		ress.User = &user
	}

	ress.PaymentServiceURL = getPaymentServiceURL()

	categories := []Category{}

	err := dbx.Select(&categories, "SELECT * FROM `categories`")
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		return
	}
	ress.Categories = categories

	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	json.NewEncoder(w).Encode(ress)
}

func postLogin(w http.ResponseWriter, r *http.Request) {
	rl := reqLogin{}
	err := json.NewDecoder(r.Body).Decode(&rl)
	if err != nil {
		outputErrorMsg(w, http.StatusBadRequest, "json decode error")
		return
	}

	accountName := rl.AccountName
	password := rl.Password

	if accountName == "" || password == "" {
		outputErrorMsg(w, http.StatusBadRequest, "all parameters are required")

		return
	}

	u := User{}
	err = dbx.Get(&u, "SELECT * FROM `users` WHERE `account_name` = ?", accountName)
	if err == sql.ErrNoRows {
		outputErrorMsg(w, http.StatusUnauthorized, "アカウント名かパスワードが間違えています")
		return
	}
	if err != nil {
		log.Print(err)

		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		return
	}

	err = bcrypt.CompareHashAndPassword(u.HashedPassword, []byte(password))
	if err == bcrypt.ErrMismatchedHashAndPassword {
		outputErrorMsg(w, http.StatusUnauthorized, "アカウント名かパスワードが間違えています")
		return
	}
	if err != nil {
		log.Print(err)

		outputErrorMsg(w, http.StatusInternalServerError, "crypt error")
		return
	}

	session := getSession(r)

	session.Values["user_id"] = u.ID
	session.Values["csrf_token"] = secureRandomStr(20)
	if err = session.Save(r, w); err != nil {
		log.Print(err)

		outputErrorMsg(w, http.StatusInternalServerError, "session error")
		return
	}

	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	json.NewEncoder(w).Encode(u)
}

func postRegister(w http.ResponseWriter, r *http.Request) {
	rr := reqRegister{}
	err := json.NewDecoder(r.Body).Decode(&rr)
	if err != nil {
		outputErrorMsg(w, http.StatusBadRequest, "json decode error")
		return
	}

	accountName := rr.AccountName
	address := rr.Address
	password := rr.Password

	if accountName == "" || password == "" || address == "" {
		outputErrorMsg(w, http.StatusBadRequest, "all parameters are required")

		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	if err != nil {
		log.Print(err)

		outputErrorMsg(w, http.StatusInternalServerError, "error")
		return
	}

	result, err := dbx.Exec("INSERT INTO `users` (`account_name`, `hashed_password`, `address`) VALUES (?, ?, ?)",
		accountName,
		hashedPassword,
		address,
	)
	if err != nil {
		log.Print(err)

		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		return
	}

	userID, err := result.LastInsertId()

	if err != nil {
		log.Print(err)

		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		return
	}

	u := User{
		ID:          userID,
		AccountName: accountName,
		Address:     address,
	}

	session := getSession(r)
	session.Values["user_id"] = u.ID
	session.Values["csrf_token"] = secureRandomStr(20)
	if err = session.Save(r, w); err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "session error")
		return
	}

	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	json.NewEncoder(w).Encode(u)
}

func getReports(w http.ResponseWriter, r *http.Request) {
	transactionEvidences := make([]TransactionEvidence, 0)
	err := dbx.Select(&transactionEvidences, "SELECT * FROM `transaction_evidences` WHERE `id` > 15007")
	if err != nil {
		log.Print(err)
		outputErrorMsg(w, http.StatusInternalServerError, "db error")
		return
	}

	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	json.NewEncoder(w).Encode(transactionEvidences)
}

func outputErrorMsg(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json;charset=utf-8")

	w.WriteHeader(status)

	json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{Error: msg})
}

func getImageURL(imageName string) string {
	return fmt.Sprintf("/upload/%s", imageName)
}
