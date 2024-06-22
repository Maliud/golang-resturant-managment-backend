package controllers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/Maliud/golang-resturant-managment-backend/database"
	"github.com/Maliud/golang-resturant-managment-backend/helpers"
	"github.com/Maliud/golang-resturant-managment-backend/models"
	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/crypto/bcrypt"
)

var userCollection *mongo.Collection = database.OpenCollection(database.Client, "user")

func GetUsers() gin.HandlerFunc {
	return func(c *gin.Context) {
		var ctx, cancel = context.WithTimeout(context.Background(), 100*time.Second)

		recordPerPage, err := strconv.Atoi(c.Query("recordPerPage"))
		if err != nil || recordPerPage < 1 {
			recordPerPage = 10
		}

		page, err1 := strconv.Atoi(c.Query("page"))
		if err1 != nil || page < 1 {
			page = 1
		}

		startIndex := (page - 1) * recordPerPage
		startIndex, err = strconv.Atoi(c.Query("startIndex"))

		matchStage := bson.D{{"$match", bson.D{{}}}}
		projectStage := bson.D{
			{"$project", bson.D{
				{"_id", 0},
				{"total_count", 1},
				{"user_items", bson.D{{"$slice", []interface{}{"$data", startIndex, recordPerPage}}}},
			}}}

		result, err := userCollection.Aggregate(ctx, mongo.Pipeline{
			matchStage, projectStage})
		defer cancel()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "kullanıcı öğeleri listelenirken hata oluştu"})
		}

		var allUsers []bson.M
		if err = result.All(ctx, &allUsers); err != nil {
			log.Fatal(err)
		}
		c.JSON(http.StatusOK, allUsers[0])
	}
}

func GetUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		var ctx, cancel = context.WithTimeout(context.Background(), 100*time.Second)
		userId := c.Param("user_id")
		var user models.User

		err := userCollection.FindOne(ctx, bson.M{"user_id": userId}).Decode(&user)
		defer cancel()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "kullanıcı öğeleri listelenirken hata oluştu"})
		}
		c.JSON(http.StatusOK, user)
	}
}

func SignUp() gin.HandlerFunc {
	return func(c *gin.Context) {
		var ctx, cancel = context.WithTimeout(context.Background(), 100*time.Second)
		var user models.User
		// postman'den gelen JSON verisini golang'ın anlayacağı bir şekle dönüştürün
		if err := c.BindJSON(&user); err != nil{
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// kullanıcı yapısına göre verileri doğrulayın
		validationErr := validate.Struct(user)
		if validationErr != nil{
			c.JSON(http.StatusBadRequest, gin.H{"error": validationErr.Error()})
			return
		}
		// e-postanın daha önce başka bir kullanıcı tarafından kullanılıp kullanılmadığını kontrol edeceksiniz
		count, err := userCollection.CountDocuments(ctx, bson.M{"email": user.Email})
		defer cancel()
		if err != nil{
			log.Panic(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "e-posta denetimi sırasında hata oluştu"})
			return
		}
		// hash şifre
		password := HashPassword(*user.Password)
		user.Password = &password
		// telefon numarasının daha önce başka bir kullanıcı tarafından kullanılıp kullanılmadığını da kontrol edeceksiniz
		count, err = userCollection.CountDocuments(ctx, bson.M{"phone": user.Phone})
		defer cancel()
		if err != nil{
			log.Panic(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "telefon numarası kontrol edilirken hata oluştu"})
			return
		}
		if count > 0 {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Bu email adresi veya telefon numarası zaten var."})
			return
		}
		// kullanıcı nesnesi için bazı ekstra ayrıntılar oluşturun - created_At, updated_at, ID
		user.Created_at, _ = time.Parse(time.RFC3339, time.Now().Format(time.RFC3339))
		user.Update_at, _ = time.Parse(time.RFC3339, time.Now().Format(time.RFC3339))
		user.ID = primitive.NewObjectID()
		user.User_id = user.ID.Hex()
		// belirteç oluştur ve belirteci yenile (yardımcıdan tüm belirteçleri oluştur işlevi)
		token, refreshToken, _ :=  helpers.GenerateAllTokens(*user.Email, *user.First_name, *user.Last_name, user.User_id)
		user.Token = &token
		user.Refresh_Token = &refreshToken
		// her şey tamamsa, bu yeni kullanıcıyı kullanıcı koleksiyonuna eklersiniz
		resultInsertionNumber, insertErr := userCollection.InsertOne(ctx, user)
		if insertErr != nil{
			msg := fmt.Sprintf("Kullanıcı Oluşturulamadı!")
			c.JSON(http.StatusInternalServerError, gin.H{"error": msg})
			return
		}
		defer cancel()
		// durumu OK olarak döndür ve sonucu geri gönder
		c.JSON(http.StatusOK, resultInsertionNumber)
	}
}

func Login() gin.HandlerFunc {
	return func(c *gin.Context) {
		var ctx, cancel = context.WithTimeout(context.Background(), 100*time.Second)
		var user models.User
		var foundUser models.User
		// postman'den gelen ve JSON formatında olan oturum açma verilerini golang tarafından okunabilir formata dönüştürün
		if err := c.BindJSON(&user); err != nil{
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// bu e-postayı gönderen bir kullanıcı bulun ve bu kullanıcının var olup olmadığına bakın
		err := userCollection.FindOne(ctx, bson.M{"email": user.Email}).Decode(&foundUser)
		defer cancel()
		if err != nil{
			c.JSON(http.StatusInternalServerError, gin.H{"error": "kullanıcı bulunamadı, lütfen giriş bilgileri kontrol ediniz..."})
			return
		}
		// sonra şifreyi doğrulayacaksınız
		passwordIsValid, msg := VerifyPassword(*user.Password, *foundUser.Password)
		
		if passwordIsValid != true{
			c.JSON(http.StatusInternalServerError, gin.H{"error": msg})
			return
		}
		// her şey yolunda giderse, token üretilir
		token, refreshToken, _ := helpers.GenerateAllTokens(*foundUser.Email, *foundUser.First_name, *foundUser.Last_name, foundUser.User_id)
		// daha sonra tokenlerı guncelle - token ve refreshToken
		helpers.UpdateAllTokens(token, refreshToken, foundUser.User_id)
		// return statusOK
		c.JSON(http.StatusOK, foundUser)
	}
}

func HashPassword(password string) string {
	btyes, err := bcrypt.GenerateFromPassword([]byte(password), 14)
	if err != nil{
		log.Panic(err)
	}

	return string(btyes)
}

func VerifyPassword(userPassword string, providedPassword string) (bool, string) {
	err := bcrypt.CompareHashAndPassword([]byte(providedPassword), []byte(userPassword))
	check := true
	msg := ""

	if err != nil{
		msg = fmt.Sprintf("kullanıcı adı veya şifre yanlış")
		check = false
	}

	return check, msg
}
