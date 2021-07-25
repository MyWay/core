package middleware

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"staticbackend/internal"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/gbrlsnchs/jwt/v3"
)

const (
	RootRole = 100
)

// Auth represents an authenticated user.
type Auth struct {
	AccountID primitive.ObjectID
	UserID    primitive.ObjectID
	Email     string
	Role      int
}

// JWTPayload contains the current user token
type JWTPayload struct {
	jwt.Payload
	Token string `json:"token,omitempty"`
}

var hs = jwt.NewHS256([]byte(os.Getenv("JWT_SECRET")))

var tokens map[string]Auth = make(map[string]Auth)

func RequireAuth(client *mongo.Client) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("Authorization")

			if len(key) == 0 {
				// if they requested a public repo we let them continue
				// to next security check.
				if strings.HasPrefix(r.URL.Path, "/db/pub_") || strings.HasPrefix(r.URL.Path, "/query/pub_") {
					a := Auth{
						AccountID: primitive.NewObjectID(),
						UserID:    primitive.NewObjectID(),
						Email:     "",
						Role:      0,
					}

					ctx := context.WithValue(r.Context(), ContextAuth, a)

					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}

				http.Error(w, "missing authorization HTTP header", http.StatusUnauthorized)
				return
			} else if strings.HasPrefix(key, "Bearer ") == false {
				http.Error(w,
					fmt.Sprintf("invalid authorization HTTP header, should be: Bearer your-token, but we got %s", key),
					http.StatusBadRequest,
				)
				return
			}

			key = strings.Replace(key, "Bearer ", "", -1)

			ctx := r.Context()

			auth, err := ValidateAuthKey(client, ctx, key)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			ctx = context.WithValue(ctx, ContextAuth, auth)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func ValidateAuthKey(client *mongo.Client, ctx context.Context, key string) (Auth, error) {
	a := Auth{}

	var pl JWTPayload
	if _, err := jwt.Verify([]byte(key), hs, &pl); err != nil {
		return a, fmt.Errorf("could not verify your authentication token: %s", err.Error())
	}

	conf, ok := ctx.Value(ContextBase).(BaseConfig)
	if !ok {
		return a, fmt.Errorf("invalid StaticBackend public token")
	}

	db := client.Database(conf.Name)

	auth, ok := tokens[pl.Token]
	if ok {
		return auth, nil
	}

	parts := strings.Split(key, "|")
	if len(parts) != 2 {
		return a, fmt.Errorf("invalid authentication token")
	}

	id, err := primitive.ObjectIDFromHex(parts[0])
	if err != nil {
		return a, fmt.Errorf("invalid API key format")
	}

	token, err := internal.FindToken(db, id, parts[1])
	if err != nil {
		return a, fmt.Errorf("error retrieving your token: %s", err.Error())
	}

	a = Auth{
		AccountID: token.AccountID,
		UserID:    token.ID,
		Email:     token.Email,
		Role:      token.Role,
	}
	tokens[pl.Token] = a

	return a, nil
}

func RequireRoot(client *mongo.Client) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("Authorization")

			if len(key) == 0 {
				http.Error(w, "missing authorization HTTP header", http.StatusUnauthorized)
				return
			} else if strings.HasPrefix(key, "Bearer ") == false {
				http.Error(w,
					fmt.Sprintf("invalid authorization HTTP header, should be: Bearer your-token, but we got %s", key),
					http.StatusBadRequest,
				)
				return
			}

			key = strings.Replace(key, "Bearer ", "", -1)

			parts := strings.Split(key, "|")
			if len(parts) != 3 {
				http.Error(w, "invalid root token", http.StatusBadRequest)
				return
			}

			id, err := primitive.ObjectIDFromHex(parts[0])
			if err != nil {
				http.Error(w, "invalid root token", http.StatusBadRequest)
				return
			}

			acctID, err := primitive.ObjectIDFromHex(parts[1])
			if err != nil {
				http.Error(w, "invalid root token", http.StatusBadRequest)
				return
			}

			token := parts[2]

			ctx := r.Context()
			conf, ok := ctx.Value(ContextBase).(BaseConfig)
			if !ok {
				http.Error(w, "invalid StaticBackend public key", http.StatusUnauthorized)
				return
			}

			db := client.Database(conf.Name)

			tok, err := internal.FindRootToken(db, id, acctID, token)
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			} else if tok.Role < RootRole {
				http.Error(w, "not enough permission", http.StatusUnauthorized)
				return
			}

			a := Auth{
				AccountID: tok.AccountID,
				UserID:    tok.ID,
				Email:     tok.Email,
				Role:      tok.Role,
			}

			ctx = context.WithValue(ctx, ContextAuth, a)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
