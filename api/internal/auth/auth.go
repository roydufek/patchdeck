package auth

import (
    "context"
    "crypto/rand"
    "encoding/base32"
    "fmt"
    "math/big"
    "net/url"
    "strings"
    "time"

    "github.com/golang-jwt/jwt/v5"
    "github.com/pquerna/otp/totp"
    "golang.org/x/crypto/bcrypt"
)

type Claims struct {
    UserID   string `json:"user_id"`
    Username string `json:"username"`
    Role     string `json:"role"`
    jwt.RegisteredClaims
}

type key int

const claimsKey key = 1

func HashPassword(password string) (string, error) {
    b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
    return string(b), err
}

func CheckPassword(hash, password string) bool {
    return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func NewTOTPSecret(issuer, account string) (secret string, uri string, err error) {
    k, err := totp.Generate(totp.GenerateOpts{Issuer: issuer, AccountName: account})
    if err != nil {
        return "", "", err
    }
    return k.Secret(), k.URL(), nil
}

func ValidateTOTP(secret, code string) bool {
    return totp.Validate(code, secret)
}

func SignJWT(secret, userID, username, role string, ttl time.Duration) (string, error) {
    now := time.Now()
    c := Claims{UserID: userID, Username: username, Role: role, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(now.Add(ttl)), IssuedAt: jwt.NewNumericDate(now)}}
    t := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
    return t.SignedString([]byte(secret))
}

func ParseJWT(secret, token string) (*Claims, error) {
    parsed, err := jwt.ParseWithClaims(token, &Claims{}, func(_ *jwt.Token) (any, error) { return []byte(secret), nil })
    if err != nil || !parsed.Valid {
        return nil, fmt.Errorf("invalid token")
    }
    c, ok := parsed.Claims.(*Claims)
    if !ok {
        return nil, fmt.Errorf("invalid claims")
    }
    return c, nil
}

func WithClaims(ctx context.Context, c *Claims) context.Context {
    return context.WithValue(ctx, claimsKey, c)
}
func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
    c, ok := ctx.Value(claimsKey).(*Claims)
    return c, ok
}

const recoveryAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func GenerateRecoveryCodes(n int) []string {
    if n <= 0 {
        n = 1
    }
    codes := make([]string, n)
    for i := 0; i < n; i++ {
        raw := make([]byte, 8)
        for j := 0; j < 8; j++ {
            idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(recoveryAlphabet))))
            if err != nil {
                raw[j] = recoveryAlphabet[j%len(recoveryAlphabet)]
                continue
            }
            raw[j] = recoveryAlphabet[idx.Int64()]
        }
        base := string(raw)
        codes[i] = fmt.Sprintf("%s-%s", base[:4], base[4:])
    }
    return codes
}

func GenerateTOTPWithSecret(issuer, account, secret string) (string, error) {
    sanitized := normalizeBase32Secret(secret)
    if sanitized == "" {
        return "", fmt.Errorf("secret required")
    }
    label := url.PathEscape(fmt.Sprintf("%s:%s", issuer, account))
    values := url.Values{}
    values.Set("secret", sanitized)
    values.Set("issuer", issuer)
    return fmt.Sprintf("otpauth://totp/%s?%s", label, values.Encode()), nil
}

func ValidateBase32Secret(secret string) bool {
    sanitized := normalizeBase32Secret(secret)
    if sanitized == "" {
        return false
    }
    decoder := base32.StdEncoding.WithPadding(base32.NoPadding)
    if _, err := decoder.DecodeString(sanitized); err == nil {
        return true
    }
    padded := sanitized
    if rem := len(padded) % 8; rem != 0 {
        padded += strings.Repeat("=", 8-rem)
    }
    _, err := base32.StdEncoding.DecodeString(padded)
    return err == nil
}

func NormalizeBase32Secret(secret string) string {
    return normalizeBase32Secret(secret)
}

func normalizeBase32Secret(secret string) string {
    cleaned := strings.ToUpper(strings.TrimSpace(secret))
    cleaned = strings.ReplaceAll(cleaned, " ", "")
    cleaned = strings.ReplaceAll(cleaned, "-", "")
    cleaned = strings.TrimRight(cleaned, "=")
    return cleaned
}
