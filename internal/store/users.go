package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID           int64
	Email        string
	Name         string
	PasswordHash string
	CreatedAt    time.Time
}

var ErrNotFound = errors.New("store: not found")

type Users struct {
	pool *pgxpool.Pool
}

func NewUsers(pool *pgxpool.Pool) *Users {
	return &Users{pool: pool}
}

func (u *Users) ByEmail(ctx context.Context, email string) (User, error) {
	row := u.pool.QueryRow(ctx,
		`SELECT id, email, name, password, created_at FROM users WHERE email=$1`, email,
	)
	return scanUser(row)
}

func (u *Users) ByID(ctx context.Context, id int64) (User, error) {
	row := u.pool.QueryRow(ctx,
		`SELECT id, email, name, password, created_at FROM users WHERE id=$1`, id,
	)
	return scanUser(row)
}

func CheckPassword(user User, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) == nil
}

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}
