package utils

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type CustomClaim struct {
	*jwt.RegisteredClaims
}

func CreateJwt(pemKey []byte, clientId string) string {
	expiration := time.Now().Add(time.Minute * 10)
	claims := CustomClaim{
		RegisteredClaims: &jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-time.Minute)),
			Issuer:    clientId,
			ExpiresAt: jwt.NewNumericDate(expiration),
			Subject:   "JWT creation",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	privateKey, _ := jwt.ParseRSAPrivateKeyFromPEM(pemKey)
	myJwt, err := token.SignedString(privateKey)
	if err != nil {
		panic(err)
	}
	return myJwt

}
