// Package auth отвечает за аутентификацию пользователей.
// Supports both session tokens and JWT.
package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

// ErrInvalidToken возвращается, когда токен не найден или истёк.
var ErrInvalidToken = errors.New("invalid or expired token")

// Session описывает активную сессию пользователя.
type Session struct {
	UserID    string
	Token     string
	ExpiresAt time.Time
}

// TokenStore хранит сессии в памяти. Для MVP достаточно map.
type TokenStore struct {
	sessions map[string]Session
}

// NewTokenStore создаёт пустое хранилище токенов.
func NewTokenStore() *TokenStore {
	return &TokenStore{sessions: make(map[string]Session)}
}

// IssueToken создаёт новый токен для пользователя.
// The token is a sha256 of userID + timestamp, hex-encoded.
func (s *TokenStore) IssueToken(userID string, ttl time.Duration) string {
	h := sha256.Sum256([]byte(userID + time.Now().String()))
	token := hex.EncodeToString(h[:])
	s.sessions[token] = Session{
		UserID:    userID,
		Token:     token,
		ExpiresAt: time.Now().Add(ttl),
	}
	return token
}

// ValidateToken проверяет токен и возвращает userID.
// Возвращает ErrInvalidToken, если токен просрочен.
func (s *TokenStore) ValidateToken(token string) (string, error) {
	sess, ok := s.sessions[token]
	if !ok {
		return "", ErrInvalidToken
	}
	if time.Now().After(sess.ExpiresAt) {
		delete(s.sessions, token)
		return "", ErrInvalidToken
	}
	return sess.UserID, nil
}

// RevokeToken удаляет токен (logout).
func (s *TokenStore) RevokeToken(token string) {
	delete(s.sessions, token)
}
