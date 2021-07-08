package main

import (
	"crypto/hmac"
	"crypto/rand"
	crand "crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/sessions"
	"github.com/jmoiron/sqlx"

	_ "net/http/pprof"
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
	templates   *template.Template
	dbx         *sqlx.DB
	store       sessions.Store
	categoryMap map[int]Category
)

type Config struct {
	Name string `json:"name" db:"name"`
	Val  string `json:"val" db:"val"`
}

type User struct {
	ID          int64  `json:"id" db:"id"`
	AccountName string `json:"account_name" db:"account_name"`
	// HashedPassword []byte    `json:"-" db:"hashed_password"`
	HashedPassword string    `json:"-" db:"hashed_password"`
	Salt           string    `json:"-" db:"salt"`
	Address        string    `json:"address,omitempty" db:"address"`
	NumSellItems   int       `json:"num_sell_items" db:"num_sell_items"`
	LastBump       time.Time `json:"-" db:"last_bump"`
	CreatedAt      time.Time `json:"-" db:"created_at"`
}

type NullableUser struct {
	ID           sql.NullInt64  `json:"id" db:"id"`
	AccountName  sql.NullString `json:"account_name" db:"account_name"`
	NumSellItems sql.NullInt64  `json:"num_sell_items" db:"num_sell_items"`
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

type ItemWithUser struct {
	ID          int64        `json:"id" db:"id"`
	SellerID    int64        `json:"seller_id" db:"seller_id"`
	BuyerID     int64        `json:"buyer_id" db:"buyer_id"`
	Status      string       `json:"status" db:"status"`
	Name        string       `json:"name" db:"name"`
	Price       int          `json:"price" db:"price"`
	Description string       `json:"description" db:"description"`
	ImageName   string       `json:"image_name" db:"image_name"`
	CategoryID  int          `json:"category_id" db:"category_id"`
	CreatedAt   time.Time    `json:"-" db:"created_at"`
	UpdatedAt   time.Time    `json:"-" db:"updated_at"`
	Seller      NullableUser `json:"-" db:"seller"`
	Buyer       NullableUser `json:"-" db:"buyer"`
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
	HasNext bool         `json:"has_next"`
	Items   []ItemDetail `json:"items"`
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
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()

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

	initCategoryMap(dbx)

	router := gin.Default()

	// template
	router.SetHTMLTemplate(templates)

	// API
	router.POST("/initialize", postInitialize)
	router.GET("/new_items.json", getNewItems)
	// router.GET("/new_items/:root_category_id.json", getNewCategoryItems)
	router.GET("/new_items/:root_category_id", getNewCategoryItems)
	router.GET("/users/transactions.json", getTransactions)
	// router.GET("/users/:user_id.json", getUserItems)
	// router.GET("/items/:item_id.json", getItem)
	router.POST("/items/edit", postItemEdit)
	router.POST("/buy", postBuy)
	router.POST("/sell", postSell)
	router.POST("/ship", postShip)
	router.POST("/ship_done", postShipDone)
	router.POST("/complete", postComplete)
	// router.GET("/transactions/:transaction_evidence_id.png", getQRCode)
	router.POST("/bump", postBump)
	router.GET("/settings", getSettings)
	router.POST("/login", postLogin)
	router.POST("/register", postRegister)
	router.GET("/reports.json", getReports)
	// Frontend
	router.GET("/", getIndex)
	router.GET("/login", getIndex)
	router.GET("/register", getIndex)
	router.GET("/timeline", getIndex)
	router.GET("/categories/:category_id/items", getIndex)
	router.GET("/sell", getIndex)
	// router.GET("/items/:item_id", getIndex)
	router.GET("/items/:item_id", handleItems)
	router.GET("/items/:item_id/edit", getIndex)
	router.GET("/items/:item_id/buy", getIndex)
	router.GET("/buy/complete", getIndex)
	// router.GET("/transactions/:transaction_id", getIndex)
	router.GET("/transactions/:transaction_id", handleTransactions)
	// router.GET("/users/:user_id", getIndex)
	router.GET("/users/:user_id", handleUsers)
	router.GET("/users/setting", getIndex)
	// Assets
	router.Static("/static", "../public/static")
	router.Static("/upload", "../public/upload")
	router.StaticFile("/index.html", "../public/index.html")
	router.StaticFile("/asset-manifest.json", "../public/asset-manifest.json")
	router.StaticFile("/manifest.json", "../public/manifest.json")
	router.StaticFile("/favicon.png", "../public/favicon.png")
	router.StaticFile("/internal_server_error.png", "../public/internal_server_error.png")
	router.StaticFile("/logo.png", "../public/logo.png")
	router.StaticFile("/not_found.png", "../public/not_found.png")
	router.StaticFile("/precache-manifest.b2bd30b977e2fb5edb9ffe534b18d478.js", "../public/precache-manifest.b2bd30b977e2fb5edb9ffe534b18d478.js")
	router.StaticFile("/service-worker.js", "../public/service-worker.js")
	log.Fatal(http.ListenAndServe(":8000", router))
}

func getSession(c *gin.Context) *sessions.Session {
	session, _ := store.Get(c.Request, sessionName)

	return session
}

func getCSRFToken(c *gin.Context) string {
	session := getSession(c)

	csrfToken, ok := session.Values["csrf_token"]
	if !ok {
		return ""
	}

	return csrfToken.(string)
}

func initCategoryMap(q sqlx.Queryer) {
	var categories []Category
	err := sqlx.Select(q, &categories, "SELECT * FROM `categories`")
	if err != nil {
		log.Fatalf("%v", err)
	}
	categoryMap = make(map[int]Category)
	for _, category := range categories {
		categoryMap[category.ID] = category
	}
}

func getUser(c *gin.Context) (user User, errCode int, errMsg string) {
	session := getSession(c)
	userID, ok := session.Values["user_id"]
	if !ok {
		return user, http.StatusNotFound, "no session"
	}

	err := dbx.Get(&user, "SELECT * FROM `users` WHERE `id` = ?", userID)
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
	err = sqlx.Get(q, &user, "SELECT `id`, `account_name`, `num_sell_items` FROM `users` WHERE `id` = ?", userID)
	if err != nil {
		return userSimple, err
	}
	userSimple.ID = user.ID
	userSimple.AccountName = user.AccountName
	userSimple.NumSellItems = user.NumSellItems
	return userSimple, err
}

func getCategoryByID(categoryID int) (Category, error) {
	category, isContain := categoryMap[categoryID]
	if !isContain {
		return category, fmt.Errorf("category not found")
	}
	if category.ParentID != 0 {
		parentCategory, err := getCategoryByID(category.ParentID)
		if err != nil {
			return category, err
		}
		category.ParentCategoryName = parentCategory.CategoryName
	}
	return category, nil
}

func getConfigByName(name string) (string, error) {
	config := Config{}
	err := dbx.Get(&config, "SELECT * FROM `configs` WHERE `name` = ?", name)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		log.Print(err)
		return "", err
	}
	return config.Val, err
}

func getPaymentServiceURL() string {
	val, _ := getConfigByName("payment_service_url")
	if val == "" {
		return DefaultPaymentServiceURL
	}
	return val
}

func getShipmentServiceURL() string {
	val, _ := getConfigByName("shipment_service_url")
	if val == "" {
		return DefaultShipmentServiceURL
	}
	return val
}

func getIndex(c *gin.Context) {
	c.HTML(http.StatusOK, "index.html", struct{}{})
}

func postInitialize(c *gin.Context) {
	ri := reqInitialize{}

	err := c.ShouldBindJSON(&ri)
	if err != nil {
		outputErrorMsg(c, http.StatusBadRequest, "json decode error")
		return
	}

	cmd := exec.Command("../sql/init.sh")
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stderr
	cmd.Run()
	if err != nil {
		outputErrorMsg(c, http.StatusInternalServerError, "exec init.sh error")
		return
	}

	_, err = dbx.Exec(
		"INSERT INTO `configs` (`name`, `val`) VALUES (?, ?) ON DUPLICATE KEY UPDATE `val` = VALUES(`val`)",
		"payment_service_url",
		ri.PaymentServiceURL,
	)
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		return
	}
	_, err = dbx.Exec(
		"INSERT INTO `configs` (`name`, `val`) VALUES (?, ?) ON DUPLICATE KEY UPDATE `val` = VALUES(`val`)",
		"shipment_service_url",
		ri.ShipmentServiceURL,
	)
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		return
	}

	initCategoryMap(dbx)

	res := resInitialize{
		// キャンペーン実施時には還元率の設定を返す。詳しくはマニュアルを参照のこと。
		Campaign: 0,
		// 実装言語を返す
		Language: "Go",
	}

	c.JSON(http.StatusOK, res)
}

func getNewItems(c *gin.Context) {
	itemIDStr := c.Query("item_id")
	var itemID int64
	var err error
	if itemIDStr != "" {
		itemID, err = strconv.ParseInt(itemIDStr, 10, 64)
		if err != nil || itemID <= 0 {
			outputErrorMsg(c, http.StatusBadRequest, "item_id param error")
			return
		}
	}

	createdAtStr := c.Query("created_at")
	var createdAt int64
	if createdAtStr != "" {
		createdAt, err = strconv.ParseInt(createdAtStr, 10, 64)
		if err != nil || createdAt <= 0 {
			outputErrorMsg(c, http.StatusBadRequest, "created_at param error")
			return
		}
	}

	items := []ItemWithUser{}
	if itemID > 0 && createdAt > 0 {
		// paging
		err := dbx.Select(&items,
			"SELECT"+
				"  `items`.*,"+
				"  `users`.`id` AS `seller.id`,"+
				"  `users`.`account_name` AS `seller.account_name`,"+
				"  `users`.`num_sell_items` AS `seller.num_sell_items` "+
				"FROM"+
				"  `items`"+
				"  LEFT JOIN"+
				"    `users`"+
				"  ON  `items`.`seller_id` = `users`.`id` "+
				"WHERE"+
				"  `items`.`status` IN(?, ?) "+
				"AND ("+
				"    `items`.`created_at` < ?"+
				"  OR  ("+
				"      `items`.`created_at` <= ?"+
				"    AND `items`.`id` < ?"+
				"    )"+
				"  ) "+
				"ORDER BY"+
				"  `items`.`created_at` DESC,"+
				"  `items`.`id` DESC "+
				"LIMIT ?",
			ItemStatusOnSale,
			ItemStatusSoldOut,
			time.Unix(createdAt, 0),
			time.Unix(createdAt, 0),
			itemID,
			ItemsPerPage+1,
		)
		if err != nil {
			log.Print(err)
			outputErrorMsg(c, http.StatusInternalServerError, "db error")
			return
		}
	} else {
		// 1st page
		err := dbx.Select(&items,
			"SELECT"+
				"  `items`.*,"+
				"  `users`.`id` AS `seller.id`,"+
				"  `users`.`account_name` AS `seller.account_name`,"+
				"  `users`.`num_sell_items` AS `seller.num_sell_items` "+
				"FROM"+
				"  `items`"+
				"  LEFT JOIN"+
				"    `users`"+
				"  ON  `items`.`seller_id` = `users`.`id` "+
				"WHERE"+
				"  `status` IN(?, ?) "+
				"ORDER BY"+
				"  `created_at` DESC,"+
				"  `id` DESC "+
				"LIMIT ?",
			ItemStatusOnSale,
			ItemStatusSoldOut,
			ItemsPerPage+1,
		)
		if err != nil {
			log.Print(err)
			outputErrorMsg(c, http.StatusInternalServerError, "db error")
			return
		}
	}

	itemSimples := []ItemSimple{}
	for _, item := range items {
		// seller, err := getUserSimpleByID(dbx, item.SellerID)
		if !item.Seller.ID.Valid {
			outputErrorMsg(c, http.StatusNotFound, "seller not found")
			return
		}
		seller := UserSimple{
			ID:           item.Seller.ID.Int64,
			AccountName:  item.Seller.AccountName.String,
			NumSellItems: int(item.Seller.NumSellItems.Int64),
		}
		category, err := getCategoryByID(item.CategoryID)
		if err != nil {
			outputErrorMsg(c, http.StatusNotFound, "category not found")
			return
		}
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

	c.JSON(http.StatusOK, rni)
}

func getNewCategoryItems(c *gin.Context) {
	// rootCategoryIDStr := c.Param("root_category_id")
	rootCategoryIDStr := strings.ReplaceAll(c.Param("root_category_id"), ".json", "")
	rootCategoryID, err := strconv.Atoi(rootCategoryIDStr)
	if err != nil || rootCategoryID <= 0 {
		outputErrorMsg(c, http.StatusBadRequest, "incorrect category id")
		return
	}

	rootCategory, err := getCategoryByID(rootCategoryID)
	if err != nil || rootCategory.ParentID != 0 {
		outputErrorMsg(c, http.StatusNotFound, "category not found")
		return
	}

	var categoryIDs []int
	err = dbx.Select(&categoryIDs, "SELECT id FROM `categories` WHERE parent_id=?", rootCategory.ID)
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		return
	}

	itemIDStr := c.Query("item_id")
	var itemID int64
	if itemIDStr != "" {
		itemID, err = strconv.ParseInt(itemIDStr, 10, 64)
		if err != nil || itemID <= 0 {
			outputErrorMsg(c, http.StatusBadRequest, "item_id param error")
			return
		}
	}

	createdAtStr := c.Query("created_at")
	var createdAt int64
	if createdAtStr != "" {
		createdAt, err = strconv.ParseInt(createdAtStr, 10, 64)
		if err != nil || createdAt <= 0 {
			outputErrorMsg(c, http.StatusBadRequest, "created_at param error")
			return
		}
	}

	var inQuery string
	var inArgs []interface{}
	if itemID > 0 && createdAt > 0 {
		// paging
		inQuery, inArgs, err = sqlx.In(
			"SELECT"+
				"  `items`.*,"+
				"  `users`.`id` AS `seller.id`,"+
				"  `users`.`account_name` AS `seller.account_name`,"+
				"  `users`.`num_sell_items` AS `seller.num_sell_items` "+
				"FROM"+
				"  `items`"+
				"  LEFT JOIN"+
				"    `users`"+
				"  ON  `items`.`seller_id` = `users`.`id` "+
				"WHERE"+
				"  `items`.`status` IN(?, ?) "+
				"AND category_id IN(?) "+
				"AND ("+
				"    `items`.`created_at` < ?"+
				"  OR  ("+
				"      `items`.`created_at` <= ?"+
				"    AND `items`.`id` < ?"+
				"    )"+
				"  ) "+
				"ORDER BY"+
				"  `items`.`created_at` DESC,"+
				"  `items`.`id` DESC "+
				"LIMIT ?",
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
			outputErrorMsg(c, http.StatusInternalServerError, "db error")
			return
		}
	} else {
		// 1st page
		inQuery, inArgs, err = sqlx.In(
			"SELECT"+
				"  `items`.*,"+
				"  `users`.`id` AS `seller.id`,"+
				"  `users`.`account_name` AS `seller.account_name`,"+
				"  `users`.`num_sell_items` AS `seller.num_sell_items` "+
				"FROM"+
				"  `items`"+
				"  LEFT JOIN"+
				"    `users`"+
				"  ON  `items`.`seller_id` = `users`.`id` "+
				"WHERE"+
				"  `items`.`status` IN(?, ?) "+
				"AND `items`.`category_id` IN(?) "+
				"ORDER BY"+
				"  `items`.`created_at` DESC,"+
				"  `items`.`id` DESC "+
				"LIMIT ?",
			ItemStatusOnSale,
			ItemStatusSoldOut,
			categoryIDs,
			ItemsPerPage+1,
		)
		if err != nil {
			log.Print(err)
			outputErrorMsg(c, http.StatusInternalServerError, "db error")
			return
		}
	}

	items := []ItemWithUser{}
	err = dbx.Select(&items, inQuery, inArgs...)

	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		return
	}

	itemSimples := []ItemSimple{}
	for _, item := range items {
		// seller, err := getUserSimpleByID(dbx, item.SellerID)
		if !item.Seller.ID.Valid {
			outputErrorMsg(c, http.StatusNotFound, "seller not found")
			return
		}
		seller := UserSimple{
			ID:           item.Seller.ID.Int64,
			AccountName:  item.Seller.AccountName.String,
			NumSellItems: int(item.Seller.NumSellItems.Int64),
		}
		category, err := getCategoryByID(item.CategoryID)
		if err != nil {
			outputErrorMsg(c, http.StatusNotFound, "category not found")
			return
		}
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

	c.JSON(http.StatusOK, rni)

}

func handleUsers(c *gin.Context) {
	userID := c.Param("user_id")
	if strings.HasSuffix(userID, ".json") {
		getUserItems(c)
	} else {
		getIndex(c)
	}
}

func getUserItems(c *gin.Context) {
	// userIDStr := c.Param("user_id")
	userIDStr := strings.ReplaceAll(c.Param("user_id"), ".json", "")
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil || userID <= 0 {
		outputErrorMsg(c, http.StatusBadRequest, "incorrect user id")
		return
	}

	userSimple, err := getUserSimpleByID(dbx, userID)
	if err != nil {
		outputErrorMsg(c, http.StatusNotFound, "user not found")
		return
	}

	itemIDStr := c.Query("item_id")
	var itemID int64
	if itemIDStr != "" {
		itemID, err = strconv.ParseInt(itemIDStr, 10, 64)
		if err != nil || itemID <= 0 {
			outputErrorMsg(c, http.StatusBadRequest, "item_id param error")
			return
		}
	}

	createdAtStr := c.Query("created_at")
	var createdAt int64
	if createdAtStr != "" {
		createdAt, err = strconv.ParseInt(createdAtStr, 10, 64)
		if err != nil || createdAt <= 0 {
			outputErrorMsg(c, http.StatusBadRequest, "created_at param error")
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
			outputErrorMsg(c, http.StatusInternalServerError, "db error")
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
			outputErrorMsg(c, http.StatusInternalServerError, "db error")
			return
		}
	}

	itemSimples := []ItemSimple{}
	for _, item := range items {
		category, err := getCategoryByID(item.CategoryID)
		if err != nil {
			outputErrorMsg(c, http.StatusNotFound, "category not found")
			return
		}
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

	c.JSON(http.StatusOK, rui)
}

func getTransactions(c *gin.Context) {

	user, errCode, errMsg := getUser(c)
	if errMsg != "" {
		outputErrorMsg(c, errCode, errMsg)
		return
	}

	itemIDStr := c.Query("item_id")
	var err error
	var itemID int64
	if itemIDStr != "" {
		itemID, err = strconv.ParseInt(itemIDStr, 10, 64)
		if err != nil || itemID <= 0 {
			outputErrorMsg(c, http.StatusBadRequest, "item_id param error")
			return
		}
	}

	createdAtStr := c.Query("created_at")
	var createdAt int64
	if createdAtStr != "" {
		createdAt, err = strconv.ParseInt(createdAtStr, 10, 64)
		if err != nil || createdAt <= 0 {
			outputErrorMsg(c, http.StatusBadRequest, "created_at param error")
			return
		}
	}

	tx := dbx.MustBegin()
	items := []ItemWithUser{}
	if itemID > 0 && createdAt > 0 {
		// paging
		err := tx.Select(&items,
			"SELECT"+
				"  `items`.*,"+
				"  `seller`.`id` AS `seller.id`,"+
				"  `seller`.`account_name` AS `seller.account_name`,"+
				"  `seller`.`num_sell_items` AS `seller.num_sell_items`,"+
				"  `buyer`.`id` AS `buyer.id`,"+
				"  `buyer`.`account_name` AS `buyer.account_name`,"+
				"  `buyer`.`num_sell_items` AS `buyer.num_sell_items` "+
				"FROM"+
				"  `items`"+
				"  LEFT JOIN"+
				"    `users` AS `seller`"+
				"  ON  `items`.`seller_id` = `seller`.`id`"+
				"  LEFT JOIN"+
				"    `users` AS `buyer`"+
				"  ON  `items`.`buyer_id` = `buyer`.`id` "+
				"WHERE"+
				"  ("+
				"    `items`.`seller_id` = ?"+
				"  OR  `items`.`buyer_id` = ?"+
				"  ) "+
				"AND `items`.`status` IN(?, ?, ?, ?, ?) "+
				"AND ("+
				"    `items`.`created_at` < ?"+
				"  OR  ("+
				"      `items`.`created_at` <= ?"+
				"    AND `items`.`id` < ?"+
				"    )"+
				"  ) "+
				"ORDER BY"+
				"  `items`.`created_at` DESC,"+
				"  `items`.`id` DESC "+
				"LIMIT ?",
			user.ID,
			user.ID,
			ItemStatusOnSale,
			ItemStatusTrading,
			ItemStatusSoldOut,
			ItemStatusCancel,
			ItemStatusStop,
			time.Unix(createdAt, 0),
			time.Unix(createdAt, 0),
			itemID,
			TransactionsPerPage+1,
		)
		if err != nil {
			log.Print(err)
			outputErrorMsg(c, http.StatusInternalServerError, "db error")
			tx.Rollback()
			return
		}
	} else {
		// 1st page
		err := tx.Select(&items,
			"SELECT"+
				"  `items`.*,"+
				"  `seller`.`id` AS `seller.id`,"+
				"  `seller`.`account_name` AS `seller.account_name`,"+
				"  `seller`.`num_sell_items` AS `seller.num_sell_items`,"+
				"  `buyer`.`id` AS `buyer.id`,"+
				"  `buyer`.`account_name` AS `buyer.account_name`,"+
				"  `buyer`.`num_sell_items` AS `buyer.num_sell_items` "+
				"FROM"+
				"  `items`"+
				"  LEFT JOIN"+
				"    `users` AS `seller`"+
				"  ON  `items`.`seller_id` = `seller`.`id`"+
				"  LEFT JOIN"+
				"    `users` AS `buyer`"+
				"  ON  `items`.`buyer_id` = `buyer`.`id` "+
				"WHERE"+
				"  ("+
				"    `items`.`seller_id` = ?"+
				"  OR  `items`.`buyer_id` = ?"+
				"  ) "+
				"AND `items`.`status` IN(?, ?, ?, ?, ?) "+
				"ORDER BY"+
				"  `items`.`created_at` DESC,"+
				"  `items`.`id` DESC "+
				"LIMIT ?",
			user.ID,
			user.ID,
			ItemStatusOnSale,
			ItemStatusTrading,
			ItemStatusSoldOut,
			ItemStatusCancel,
			ItemStatusStop,
			TransactionsPerPage+1,
		)
		if err != nil {
			log.Print(err)
			outputErrorMsg(c, http.StatusInternalServerError, "db error")
			tx.Rollback()
			return
		}
	}

	itemDetails := []ItemDetail{}
	for _, item := range items {
		// seller, err := getUserSimpleByID(tx, item.SellerID)
		if !item.Seller.ID.Valid {
			outputErrorMsg(c, http.StatusNotFound, "seller not found")
			tx.Rollback()
			return
		}
		seller := UserSimple{
			ID:           item.Seller.ID.Int64,
			AccountName:  item.Seller.AccountName.String,
			NumSellItems: int(item.Seller.NumSellItems.Int64),
		}
		category, err := getCategoryByID(item.CategoryID)
		if err != nil {
			outputErrorMsg(c, http.StatusNotFound, "category not found")
			tx.Rollback()
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

		if item.BuyerID != 0 {
			// buyer, err := getUserSimpleByID(tx, item.BuyerID)
			if !item.Buyer.ID.Valid {
				outputErrorMsg(c, http.StatusNotFound, "buyer not found")
				tx.Rollback()
				return
			}
			buyer := UserSimple{
				ID:           item.Buyer.ID.Int64,
				AccountName:  item.Buyer.AccountName.String,
				NumSellItems: int(item.Buyer.NumSellItems.Int64),
			}
			itemDetail.BuyerID = item.BuyerID
			itemDetail.Buyer = &buyer
		}

		transactionEvidence := TransactionEvidence{}
		err = tx.Get(&transactionEvidence, "SELECT * FROM `transaction_evidences` WHERE `item_id` = ?", item.ID)
		if err != nil && err != sql.ErrNoRows {
			// It's able to ignore ErrNoRows
			log.Print(err)
			outputErrorMsg(c, http.StatusInternalServerError, "db error")
			tx.Rollback()
			return
		}

		if transactionEvidence.ID > 0 {
			shipping := Shipping{}
			err = tx.Get(&shipping, "SELECT * FROM `shippings` WHERE `transaction_evidence_id` = ?", transactionEvidence.ID)
			if err == sql.ErrNoRows {
				outputErrorMsg(c, http.StatusNotFound, "shipping not found")
				tx.Rollback()
				return
			}
			if err != nil {
				log.Print(err)
				outputErrorMsg(c, http.StatusInternalServerError, "db error")
				tx.Rollback()
				return
			}
			ssr, err := APIShipmentStatus(getShipmentServiceURL(), &APIShipmentStatusReq{
				ReserveID: shipping.ReserveID,
			})
			if err != nil {
				log.Print(err)
				outputErrorMsg(c, http.StatusInternalServerError, "failed to request to shipment service")
				tx.Rollback()
				return
			}

			itemDetail.TransactionEvidenceID = transactionEvidence.ID
			itemDetail.TransactionEvidenceStatus = transactionEvidence.Status
			itemDetail.ShippingStatus = ssr.Status
		}

		itemDetails = append(itemDetails, itemDetail)
	}
	tx.Commit()

	hasNext := false
	if len(itemDetails) > TransactionsPerPage {
		hasNext = true
		itemDetails = itemDetails[0:TransactionsPerPage]
	}

	rts := resTransactions{
		Items:   itemDetails,
		HasNext: hasNext,
	}

	c.JSON(http.StatusOK, rts)

}

func handleItems(c *gin.Context) {
	itemID := c.Param("item_id")
	if strings.HasSuffix(itemID, ".json") {
		getItem(c)
	} else {
		getIndex(c)
	}
}

func getItem(c *gin.Context) {
	// itemIDStr := c.Param("item_id")
	itemIDStr := strings.ReplaceAll(c.Param("item_id"), ".json", "")
	itemID, err := strconv.ParseInt(itemIDStr, 10, 64)
	if err != nil || itemID <= 0 {
		outputErrorMsg(c, http.StatusBadRequest, "incorrect item id")
		return
	}

	user, errCode, errMsg := getUser(c)
	if errMsg != "" {
		outputErrorMsg(c, errCode, errMsg)
		return
	}

	item := Item{}
	err = dbx.Get(&item, "SELECT * FROM `items` WHERE `id` = ?", itemID)
	if err == sql.ErrNoRows {
		outputErrorMsg(c, http.StatusNotFound, "item not found")
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		return
	}

	category, err := getCategoryByID(item.CategoryID)
	if err != nil {
		outputErrorMsg(c, http.StatusNotFound, "category not found")
		return
	}

	seller, err := getUserSimpleByID(dbx, item.SellerID)
	if err != nil {
		outputErrorMsg(c, http.StatusNotFound, "seller not found")
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
			outputErrorMsg(c, http.StatusNotFound, "buyer not found")
			return
		}
		itemDetail.BuyerID = item.BuyerID
		itemDetail.Buyer = &buyer

		transactionEvidence := TransactionEvidence{}
		err = dbx.Get(&transactionEvidence, "SELECT * FROM `transaction_evidences` WHERE `item_id` = ?", item.ID)
		if err != nil && err != sql.ErrNoRows {
			// It's able to ignore ErrNoRows
			log.Print(err)
			outputErrorMsg(c, http.StatusInternalServerError, "db error")
			return
		}

		if transactionEvidence.ID > 0 {
			shipping := Shipping{}
			err = dbx.Get(&shipping, "SELECT * FROM `shippings` WHERE `transaction_evidence_id` = ?", transactionEvidence.ID)
			if err == sql.ErrNoRows {
				outputErrorMsg(c, http.StatusNotFound, "shipping not found")
				return
			}
			if err != nil {
				log.Print(err)
				outputErrorMsg(c, http.StatusInternalServerError, "db error")
				return
			}

			itemDetail.TransactionEvidenceID = transactionEvidence.ID
			itemDetail.TransactionEvidenceStatus = transactionEvidence.Status
			itemDetail.ShippingStatus = shipping.Status
		}
	}

	c.JSON(http.StatusOK, itemDetail)
}

func postItemEdit(c *gin.Context) {
	rie := reqItemEdit{}
	err := c.ShouldBindJSON(&rie)
	if err != nil {
		outputErrorMsg(c, http.StatusBadRequest, "json decode error")
		return
	}

	csrfToken := rie.CSRFToken
	itemID := rie.ItemID
	price := rie.ItemPrice

	if csrfToken != getCSRFToken(c) {
		outputErrorMsg(c, http.StatusUnprocessableEntity, "csrf token error")

		return
	}

	if price < ItemMinPrice || price > ItemMaxPrice {
		outputErrorMsg(c, http.StatusBadRequest, ItemPriceErrMsg)
		return
	}

	seller, errCode, errMsg := getUser(c)
	if errMsg != "" {
		outputErrorMsg(c, errCode, errMsg)
		return
	}

	targetItem := Item{}
	err = dbx.Get(&targetItem, "SELECT * FROM `items` WHERE `id` = ?", itemID)
	if err == sql.ErrNoRows {
		outputErrorMsg(c, http.StatusNotFound, "item not found")
		return
	}
	if err != nil {
		log.Print(err)

		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		return
	}

	if targetItem.SellerID != seller.ID {
		outputErrorMsg(c, http.StatusForbidden, "自分の商品以外は編集できません")
		return
	}

	tx := dbx.MustBegin()
	err = tx.Get(&targetItem, "SELECT * FROM `items` WHERE `id` = ? FOR UPDATE", itemID)
	if err != nil {
		log.Print(err)

		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	if targetItem.Status != ItemStatusOnSale {
		outputErrorMsg(c, http.StatusForbidden, "販売中の商品以外編集できません")
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

		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	err = tx.Get(&targetItem, "SELECT * FROM `items` WHERE `id` = ?", itemID)
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	tx.Commit()

	c.JSON(http.StatusOK, &resItemEdit{
		ItemID:        targetItem.ID,
		ItemPrice:     targetItem.Price,
		ItemCreatedAt: targetItem.CreatedAt.Unix(),
		ItemUpdatedAt: targetItem.UpdatedAt.Unix(),
	})
}

func handleTransactions(c *gin.Context) {
	transactionID := c.Param("transaction_id")
	if strings.HasSuffix(transactionID, ".png") {
		getQRCode(c)
	} else {
		getIndex(c)
	}
}

func getQRCode(c *gin.Context) {
	// transactionEvidenceIDStr := c.Param("transaction_evidence_id")
	transactionEvidenceIDStr := strings.ReplaceAll(c.Param("transaction_id"), ".png", "")
	transactionEvidenceID, err := strconv.ParseInt(transactionEvidenceIDStr, 10, 64)
	if err != nil || transactionEvidenceID <= 0 {
		outputErrorMsg(c, http.StatusBadRequest, "incorrect transaction_evidence id")
		return
	}

	seller, errCode, errMsg := getUser(c)
	if errMsg != "" {
		outputErrorMsg(c, errCode, errMsg)
		return
	}

	transactionEvidence := TransactionEvidence{}
	err = dbx.Get(&transactionEvidence, "SELECT * FROM `transaction_evidences` WHERE `id` = ?", transactionEvidenceID)
	if err == sql.ErrNoRows {
		outputErrorMsg(c, http.StatusNotFound, "transaction_evidences not found")
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		return
	}

	if transactionEvidence.SellerID != seller.ID {
		outputErrorMsg(c, http.StatusForbidden, "権限がありません")
		return
	}

	shipping := Shipping{}
	err = dbx.Get(&shipping, "SELECT * FROM `shippings` WHERE `transaction_evidence_id` = ?", transactionEvidence.ID)
	if err == sql.ErrNoRows {
		outputErrorMsg(c, http.StatusNotFound, "shippings not found")
		return
	}
	if err != nil {
		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		return
	}

	if shipping.Status != ShippingsStatusWaitPickup && shipping.Status != ShippingsStatusShipping {
		outputErrorMsg(c, http.StatusForbidden, "qrcode not available")
		return
	}

	if len(shipping.ImgBinary) == 0 {
		outputErrorMsg(c, http.StatusInternalServerError, "empty qrcode image")
		return
	}

	c.Data(http.StatusOK, "image/png", shipping.ImgBinary)
}

func postBuy(c *gin.Context) {
	rb := reqBuy{}

	err := c.ShouldBindJSON(&rb)
	if err != nil {
		outputErrorMsg(c, http.StatusBadRequest, "json decode error")
		return
	}

	if rb.CSRFToken != getCSRFToken(c) {
		outputErrorMsg(c, http.StatusUnprocessableEntity, "csrf token error")

		return
	}

	buyer, errCode, errMsg := getUser(c)
	if errMsg != "" {
		outputErrorMsg(c, errCode, errMsg)
		return
	}

	tx := dbx.MustBegin()

	targetItem := Item{}
	err = tx.Get(&targetItem, "SELECT * FROM `items` WHERE `id` = ? FOR UPDATE", rb.ItemID)
	if err == sql.ErrNoRows {
		outputErrorMsg(c, http.StatusNotFound, "item not found")
		tx.Rollback()
		return
	}
	if err != nil {
		log.Print(err)

		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	if targetItem.Status != ItemStatusOnSale {
		outputErrorMsg(c, http.StatusForbidden, "item is not for sale")
		tx.Rollback()
		return
	}

	if targetItem.SellerID == buyer.ID {
		outputErrorMsg(c, http.StatusForbidden, "自分の商品は買えません")
		tx.Rollback()
		return
	}

	seller := User{}
	err = tx.Get(&seller, "SELECT * FROM `users` WHERE `id` = ? FOR UPDATE", targetItem.SellerID)
	if err == sql.ErrNoRows {
		outputErrorMsg(c, http.StatusNotFound, "seller not found")
		tx.Rollback()
		return
	}
	if err != nil {
		log.Print(err)

		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	category, err := getCategoryByID(targetItem.CategoryID)
	if err != nil {
		log.Print(err)

		outputErrorMsg(c, http.StatusInternalServerError, "category id error")
		tx.Rollback()
		return
	}

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

		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	transactionEvidenceID, err := result.LastInsertId()
	if err != nil {
		log.Print(err)

		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	_, err = tx.Exec("UPDATE `items` SET `buyer_id` = ?, `status` = ?, `updated_at` = ? WHERE `id` = ?",
		buyer.ID,
		ItemStatusTrading,
		time.Now(),
		targetItem.ID,
	)
	if err != nil {
		log.Print(err)

		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	scr, err := APIShipmentCreate(getShipmentServiceURL(), &APIShipmentCreateReq{
		ToAddress:   buyer.Address,
		ToName:      buyer.AccountName,
		FromAddress: seller.Address,
		FromName:    seller.AccountName,
	})
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "failed to request to shipment service")
		tx.Rollback()

		return
	}

	pstr, err := APIPaymentToken(getPaymentServiceURL(), &APIPaymentServiceTokenReq{
		ShopID: PaymentServiceIsucariShopID,
		Token:  rb.Token,
		APIKey: PaymentServiceIsucariAPIKey,
		Price:  targetItem.Price,
	})
	if err != nil {
		log.Print(err)

		outputErrorMsg(c, http.StatusInternalServerError, "payment service is failed")
		tx.Rollback()
		return
	}

	if pstr.Status == "invalid" {
		outputErrorMsg(c, http.StatusBadRequest, "カード情報に誤りがあります")
		tx.Rollback()
		return
	}

	if pstr.Status == "fail" {
		outputErrorMsg(c, http.StatusBadRequest, "カードの残高が足りません")
		tx.Rollback()
		return
	}

	if pstr.Status != "ok" {
		outputErrorMsg(c, http.StatusBadRequest, "想定外のエラー")
		tx.Rollback()
		return
	}

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

		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	tx.Commit()

	c.JSON(http.StatusOK, resBuy{TransactionEvidenceID: transactionEvidenceID})
}

func postShip(c *gin.Context) {
	reqps := reqPostShip{}

	err := c.ShouldBindJSON(&reqps)
	if err != nil {
		outputErrorMsg(c, http.StatusBadRequest, "json decode error")
		return
	}

	csrfToken := reqps.CSRFToken
	itemID := reqps.ItemID

	if csrfToken != getCSRFToken(c) {
		outputErrorMsg(c, http.StatusUnprocessableEntity, "csrf token error")

		return
	}

	seller, errCode, errMsg := getUser(c)
	if errMsg != "" {
		outputErrorMsg(c, errCode, errMsg)
		return
	}

	transactionEvidence := TransactionEvidence{}
	err = dbx.Get(&transactionEvidence, "SELECT * FROM `transaction_evidences` WHERE `item_id` = ?", itemID)
	if err == sql.ErrNoRows {
		outputErrorMsg(c, http.StatusNotFound, "transaction_evidences not found")
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "db error")

		return
	}

	if transactionEvidence.SellerID != seller.ID {
		outputErrorMsg(c, http.StatusForbidden, "権限がありません")
		return
	}

	tx := dbx.MustBegin()

	item := Item{}
	err = tx.Get(&item, "SELECT * FROM `items` WHERE `id` = ? FOR UPDATE", itemID)
	if err == sql.ErrNoRows {
		outputErrorMsg(c, http.StatusNotFound, "item not found")
		tx.Rollback()
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	if item.Status != ItemStatusTrading {
		outputErrorMsg(c, http.StatusForbidden, "商品が取引中ではありません")
		tx.Rollback()
		return
	}

	err = tx.Get(&transactionEvidence, "SELECT * FROM `transaction_evidences` WHERE `id` = ? FOR UPDATE", transactionEvidence.ID)
	if err == sql.ErrNoRows {
		outputErrorMsg(c, http.StatusNotFound, "transaction_evidences not found")
		tx.Rollback()
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	if transactionEvidence.Status != TransactionEvidenceStatusWaitShipping {
		outputErrorMsg(c, http.StatusForbidden, "準備ができていません")
		tx.Rollback()
		return
	}

	shipping := Shipping{}
	err = tx.Get(&shipping, "SELECT * FROM `shippings` WHERE `transaction_evidence_id` = ? FOR UPDATE", transactionEvidence.ID)
	if err == sql.ErrNoRows {
		outputErrorMsg(c, http.StatusNotFound, "shippings not found")
		tx.Rollback()
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	img, err := APIShipmentRequest(getShipmentServiceURL(), &APIShipmentRequestReq{
		ReserveID: shipping.ReserveID,
	})
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "failed to request to shipment service")
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

		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	tx.Commit()

	rps := resPostShip{
		Path:      fmt.Sprintf("/transactions/%d.png", transactionEvidence.ID),
		ReserveID: shipping.ReserveID,
	}
	c.JSON(http.StatusOK, rps)
}

func postShipDone(c *gin.Context) {
	reqpsd := reqPostShipDone{}

	err := c.ShouldBindJSON(&reqpsd)
	if err != nil {
		outputErrorMsg(c, http.StatusBadRequest, "json decode error")
		return
	}

	csrfToken := reqpsd.CSRFToken
	itemID := reqpsd.ItemID

	if csrfToken != getCSRFToken(c) {
		outputErrorMsg(c, http.StatusUnprocessableEntity, "csrf token error")

		return
	}

	seller, errCode, errMsg := getUser(c)
	if errMsg != "" {
		outputErrorMsg(c, errCode, errMsg)
		return
	}

	transactionEvidence := TransactionEvidence{}
	err = dbx.Get(&transactionEvidence, "SELECT * FROM `transaction_evidences` WHERE `item_id` = ?", itemID)
	if err == sql.ErrNoRows {
		outputErrorMsg(c, http.StatusNotFound, "transaction_evidence not found")
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "db error")

		return
	}

	if transactionEvidence.SellerID != seller.ID {
		outputErrorMsg(c, http.StatusForbidden, "権限がありません")
		return
	}

	tx := dbx.MustBegin()

	item := Item{}
	err = tx.Get(&item, "SELECT * FROM `items` WHERE `id` = ? FOR UPDATE", itemID)
	if err == sql.ErrNoRows {
		outputErrorMsg(c, http.StatusNotFound, "items not found")
		tx.Rollback()
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	if item.Status != ItemStatusTrading {
		outputErrorMsg(c, http.StatusForbidden, "商品が取引中ではありません")
		tx.Rollback()
		return
	}

	err = tx.Get(&transactionEvidence, "SELECT * FROM `transaction_evidences` WHERE `id` = ? FOR UPDATE", transactionEvidence.ID)
	if err == sql.ErrNoRows {
		outputErrorMsg(c, http.StatusNotFound, "transaction_evidences not found")
		tx.Rollback()
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	if transactionEvidence.Status != TransactionEvidenceStatusWaitShipping {
		outputErrorMsg(c, http.StatusForbidden, "準備ができていません")
		tx.Rollback()
		return
	}

	shipping := Shipping{}
	err = tx.Get(&shipping, "SELECT * FROM `shippings` WHERE `transaction_evidence_id` = ? FOR UPDATE", transactionEvidence.ID)
	if err == sql.ErrNoRows {
		outputErrorMsg(c, http.StatusNotFound, "shippings not found")
		tx.Rollback()
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	ssr, err := APIShipmentStatus(getShipmentServiceURL(), &APIShipmentStatusReq{
		ReserveID: shipping.ReserveID,
	})
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "failed to request to shipment service")
		tx.Rollback()

		return
	}

	if !(ssr.Status == ShippingsStatusShipping || ssr.Status == ShippingsStatusDone) {
		outputErrorMsg(c, http.StatusForbidden, "shipment service側で配送中か配送完了になっていません")
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

		outputErrorMsg(c, http.StatusInternalServerError, "db error")
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

		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	tx.Commit()

	c.JSON(http.StatusOK, resBuy{TransactionEvidenceID: transactionEvidence.ID})
}

func postComplete(c *gin.Context) {
	reqpc := reqPostComplete{}

	err := c.ShouldBindJSON(&reqpc)
	if err != nil {
		outputErrorMsg(c, http.StatusBadRequest, "json decode error")
		return
	}

	csrfToken := reqpc.CSRFToken
	itemID := reqpc.ItemID

	if csrfToken != getCSRFToken(c) {
		outputErrorMsg(c, http.StatusUnprocessableEntity, "csrf token error")

		return
	}

	buyer, errCode, errMsg := getUser(c)
	if errMsg != "" {
		outputErrorMsg(c, errCode, errMsg)
		return
	}

	transactionEvidence := TransactionEvidence{}
	err = dbx.Get(&transactionEvidence, "SELECT * FROM `transaction_evidences` WHERE `item_id` = ?", itemID)
	if err == sql.ErrNoRows {
		outputErrorMsg(c, http.StatusNotFound, "transaction_evidence not found")
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "db error")

		return
	}

	if transactionEvidence.BuyerID != buyer.ID {
		outputErrorMsg(c, http.StatusForbidden, "権限がありません")
		return
	}

	tx := dbx.MustBegin()
	item := Item{}
	err = tx.Get(&item, "SELECT * FROM `items` WHERE `id` = ? FOR UPDATE", itemID)
	if err == sql.ErrNoRows {
		outputErrorMsg(c, http.StatusNotFound, "items not found")
		tx.Rollback()
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	if item.Status != ItemStatusTrading {
		outputErrorMsg(c, http.StatusForbidden, "商品が取引中ではありません")
		tx.Rollback()
		return
	}

	err = tx.Get(&transactionEvidence, "SELECT * FROM `transaction_evidences` WHERE `item_id` = ? FOR UPDATE", itemID)
	if err == sql.ErrNoRows {
		outputErrorMsg(c, http.StatusNotFound, "transaction_evidences not found")
		tx.Rollback()
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	if transactionEvidence.Status != TransactionEvidenceStatusWaitDone {
		outputErrorMsg(c, http.StatusForbidden, "準備ができていません")
		tx.Rollback()
		return
	}

	shipping := Shipping{}
	err = tx.Get(&shipping, "SELECT * FROM `shippings` WHERE `transaction_evidence_id` = ? FOR UPDATE", transactionEvidence.ID)
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	ssr, err := APIShipmentStatus(getShipmentServiceURL(), &APIShipmentStatusReq{
		ReserveID: shipping.ReserveID,
	})
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "failed to request to shipment service")
		tx.Rollback()

		return
	}

	if !(ssr.Status == ShippingsStatusDone) {
		outputErrorMsg(c, http.StatusBadRequest, "shipment service側で配送完了になっていません")
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

		outputErrorMsg(c, http.StatusInternalServerError, "db error")
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

		outputErrorMsg(c, http.StatusInternalServerError, "db error")
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

		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	tx.Commit()

	c.JSON(http.StatusOK, resBuy{TransactionEvidenceID: transactionEvidence.ID})
}

func postSell(c *gin.Context) {
	csrfToken := c.PostForm("csrf_token")
	name := c.PostForm("name")
	description := c.PostForm("description")
	priceStr := c.PostForm("price")
	categoryIDStr := c.PostForm("category_id")

	file, err := c.FormFile("image")
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusBadRequest, "image error")
		return
	}
	f, err := file.Open()
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusBadRequest, "image error")
		return
	}
	defer f.Close()

	if csrfToken != getCSRFToken(c) {
		outputErrorMsg(c, http.StatusUnprocessableEntity, "csrf token error")
		return
	}

	categoryID, err := strconv.Atoi(categoryIDStr)
	if err != nil || categoryID < 0 {
		outputErrorMsg(c, http.StatusBadRequest, "category id error")
		return
	}

	price, err := strconv.Atoi(priceStr)
	if err != nil {
		outputErrorMsg(c, http.StatusBadRequest, "price error")
		return
	}

	if name == "" || description == "" || price == 0 || categoryID == 0 {
		outputErrorMsg(c, http.StatusBadRequest, "all parameters are required")

		return
	}

	if price < ItemMinPrice || price > ItemMaxPrice {
		outputErrorMsg(c, http.StatusBadRequest, ItemPriceErrMsg)

		return
	}

	category, err := getCategoryByID(categoryID)
	if err != nil || category.ParentID == 0 {
		log.Print(categoryID, category)
		outputErrorMsg(c, http.StatusBadRequest, "Incorrect category ID")
		return
	}

	user, errCode, errMsg := getUser(c)
	if errMsg != "" {
		outputErrorMsg(c, errCode, errMsg)
		return
	}

	img, err := ioutil.ReadAll(f)
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "image error")
		return
	}

	ext := filepath.Ext(file.Filename)

	if !(ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".gif") {
		outputErrorMsg(c, http.StatusBadRequest, "unsupported image format error")
		return
	}

	if ext == ".jpeg" {
		ext = ".jpg"
	}

	imgName := fmt.Sprintf("%s%s", secureRandomStr(16), ext)
	err = ioutil.WriteFile(fmt.Sprintf("../public/upload/%s", imgName), img, 0644)
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "Saving image failed")
		return
	}

	tx := dbx.MustBegin()

	seller := User{}
	err = tx.Get(&seller, "SELECT * FROM `users` WHERE `id` = ? FOR UPDATE", user.ID)
	if err == sql.ErrNoRows {
		outputErrorMsg(c, http.StatusNotFound, "user not found")
		tx.Rollback()
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	result, err := tx.Exec("INSERT INTO `items` (`seller_id`, `status`, `name`, `price`, `description`,`image_name`,`category_id`) VALUES (?, ?, ?, ?, ?, ?, ?)",
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

		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		return
	}

	itemID, err := result.LastInsertId()
	if err != nil {
		log.Print(err)

		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		return
	}

	now := time.Now()
	_, err = tx.Exec("UPDATE `users` SET `num_sell_items`=?, `last_bump`=? WHERE `id`=?",
		seller.NumSellItems+1,
		now,
		seller.ID,
	)
	if err != nil {
		log.Print(err)

		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		return
	}
	tx.Commit()

	c.JSON(http.StatusOK, resSell{ID: itemID})
}

func secureRandomStr(b int) string {
	k := make([]byte, b)
	if _, err := crand.Read(k); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", k)
}

func postBump(c *gin.Context) {
	rb := reqBump{}
	err := c.ShouldBindJSON(&rb)
	if err != nil {
		outputErrorMsg(c, http.StatusBadRequest, "json decode error")
		return
	}

	csrfToken := rb.CSRFToken
	itemID := rb.ItemID

	if csrfToken != getCSRFToken(c) {
		outputErrorMsg(c, http.StatusUnprocessableEntity, "csrf token error")
		return
	}

	user, errCode, errMsg := getUser(c)
	if errMsg != "" {
		outputErrorMsg(c, errCode, errMsg)
		return
	}

	tx := dbx.MustBegin()

	targetItem := Item{}
	err = tx.Get(&targetItem, "SELECT * FROM `items` WHERE `id` = ? FOR UPDATE", itemID)
	if err == sql.ErrNoRows {
		outputErrorMsg(c, http.StatusNotFound, "item not found")
		tx.Rollback()
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	if targetItem.SellerID != user.ID {
		outputErrorMsg(c, http.StatusForbidden, "自分の商品以外は編集できません")
		tx.Rollback()
		return
	}

	seller := User{}
	err = tx.Get(&seller, "SELECT * FROM `users` WHERE `id` = ? FOR UPDATE", user.ID)
	if err == sql.ErrNoRows {
		outputErrorMsg(c, http.StatusNotFound, "user not found")
		tx.Rollback()
		return
	}
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	now := time.Now()
	// last_bump + 3s > now
	if seller.LastBump.Add(BumpChargeSeconds).After(now) {
		outputErrorMsg(c, http.StatusForbidden, "Bump not allowed")
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
		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		return
	}

	_, err = tx.Exec("UPDATE `users` SET `last_bump`=? WHERE id=?",
		now,
		seller.ID,
	)
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		return
	}

	err = tx.Get(&targetItem, "SELECT * FROM `items` WHERE `id` = ?", itemID)
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		tx.Rollback()
		return
	}

	tx.Commit()

	c.JSON(http.StatusOK, &resItemEdit{
		ItemID:        targetItem.ID,
		ItemPrice:     targetItem.Price,
		ItemCreatedAt: targetItem.CreatedAt.Unix(),
		ItemUpdatedAt: targetItem.UpdatedAt.Unix(),
	})
}

func getSettings(c *gin.Context) {
	csrfToken := getCSRFToken(c)

	user, _, errMsg := getUser(c)

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
		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		return
	}
	ress.Categories = categories

	c.JSON(http.StatusOK, ress)
}

func postLogin(c *gin.Context) {
	rl := reqLogin{}
	err := c.ShouldBindJSON(&rl)
	if err != nil {
		outputErrorMsg(c, http.StatusBadRequest, "json decode error")
		return
	}

	accountName := rl.AccountName
	password := rl.Password

	if accountName == "" || password == "" {
		outputErrorMsg(c, http.StatusBadRequest, "all parameters are required")

		return
	}

	u := User{}
	err = dbx.Get(&u, "SELECT * FROM `users` WHERE `account_name` = ?", accountName)
	if err == sql.ErrNoRows {
		outputErrorMsg(c, http.StatusUnauthorized, "アカウント名かパスワードが間違えています")
		return
	}
	if err != nil {
		log.Print(err)

		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		return
	}

	h := hmac.New(sha256.New, []byte(u.Salt))
	h.Write([]byte(password))
	// err = bcrypt.CompareHashAndPassword(u.HashedPassword, []byte(password))
	// if err == bcrypt.ErrMismatchedHashAndPassword {
	if u.HashedPassword != hex.EncodeToString(h.Sum(nil)) {
		outputErrorMsg(c, http.StatusUnauthorized, "アカウント名かパスワードが間違えています")
		return
	}
	if err != nil {
		log.Print(err)

		outputErrorMsg(c, http.StatusInternalServerError, "crypt error")
		return
	}

	session := getSession(c)

	session.Values["user_id"] = u.ID
	session.Values["csrf_token"] = secureRandomStr(20)
	if err = session.Save(c.Request, c.Writer); err != nil {
		log.Print(err)

		outputErrorMsg(c, http.StatusInternalServerError, "session error")
		return
	}

	c.JSON(http.StatusOK, u)
}

func postRegister(c *gin.Context) {
	rr := reqRegister{}
	err := c.ShouldBindJSON(&rr)
	if err != nil {
		outputErrorMsg(c, http.StatusBadRequest, "json decode error")
		return
	}

	accountName := rr.AccountName
	address := rr.Address
	password := rr.Password

	if accountName == "" || password == "" || address == "" {
		outputErrorMsg(c, http.StatusBadRequest, "all parameters are required")

		return
	}

	unencodedSalt := make([]byte, 16)
	_, err = io.ReadFull(rand.Reader, unencodedSalt)
	if err != nil {
		log.Print(err)

		outputErrorMsg(c, http.StatusInternalServerError, "error")
		return
	}
	encoding := base64.NewEncoding("./ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789")
	n := encoding.EncodedLen(len(unencodedSalt))
	salt := make([]byte, n)
	encoding.Encode(salt, unencodedSalt)
	for salt[n-1] == '=' {
		n--
	}

	h := hmac.New(sha256.New, []byte(salt[:n]))
	h.Write([]byte(password))
	hashedPassword := hex.EncodeToString(h.Sum(nil))

	// hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	// if err != nil {
	// 	log.Print(err)

	// 	outputErrorMsg(c, http.StatusInternalServerError, "error")
	// 	return
	// }

	result, err := dbx.Exec("INSERT INTO `users` (`account_name`, `salt`, `hashed_password`, `address`) VALUES (?, ?, ?, ?)",
		accountName,
		hashedPassword,
		salt[:n],
		address,
	)
	if err != nil {
		log.Print(err)

		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		return
	}

	userID, err := result.LastInsertId()

	if err != nil {
		log.Print(err)

		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		return
	}

	u := User{
		ID:          userID,
		AccountName: accountName,
		Address:     address,
	}

	session := getSession(c)
	session.Values["user_id"] = u.ID
	session.Values["csrf_token"] = secureRandomStr(20)
	if err = session.Save(c.Request, c.Writer); err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "session error")
		return
	}

	c.JSON(http.StatusOK, u)
}

func getReports(c *gin.Context) {
	transactionEvidences := make([]TransactionEvidence, 0)
	err := dbx.Select(&transactionEvidences, "SELECT * FROM `transaction_evidences` WHERE `id` > 15007")
	if err != nil {
		log.Print(err)
		outputErrorMsg(c, http.StatusInternalServerError, "db error")
		return
	}

	c.JSON(http.StatusOK, transactionEvidences)
}

func outputErrorMsg(c *gin.Context, status int, msg string) {
	c.JSON(status, struct {
		Error string `json:"error"`
	}{Error: msg})
}

func getImageURL(imageName string) string {
	return fmt.Sprintf("/upload/%s", imageName)
}
