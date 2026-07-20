import os
import re

# 1. Update tcp_proxy.go
proxy_path = r"internal/infrastructure/server/tcp_proxy.go"
with open(proxy_path, "r", encoding="utf-8") as f:
    content = f.read()

# Remove import
content = re.sub(r'\n\s*sqlparser "vitess\.io/vitess/go/vt/sqlparser"', "", content)
# Remove regex
content = re.sub(r'var fallbackDestructiveRegex = regexp\.MustCompile\(`\(\?i\)\\b\(DROP\|DELETE\|TRUNCATE\|ALTER\)\\b`\)\n', "", content)

# Update struct
struct_replacement = """type TCPProxy struct {
	useCase        *usecase.SessionUseCase
	mode           string
	idleTimeout    time.Duration
	rateLimit      int
	jailRepo       domain.JailRepository
	queryInspector usecase.QueryInspector
}"""
content = re.sub(r'type TCPProxy struct \{[^}]+\}', struct_replacement, content)

# Update constructor
constructor_sig = r'func NewTCPProxy\(useCase \*usecase\.SessionUseCase, mode string, idleTimeout time\.Duration, rateLimit int, jailRepo domain\.JailRepository\) \*TCPProxy \{'
constructor_replacement = """func NewTCPProxy(useCase *usecase.SessionUseCase, mode string, idleTimeout time.Duration, rateLimit int, jailRepo domain.JailRepository, queryInspector usecase.QueryInspector) *TCPProxy {
	return &TCPProxy{
		useCase:        useCase,
		mode:           mode,
		idleTimeout:    idleTimeout,
		rateLimit:      rateLimit,
		jailRepo:       jailRepo,
		queryInspector: queryInspector,
	}"""
content = re.sub(constructor_sig + r'[^}]+}', constructor_replacement, content)

# Update inspectQuery call
content = content.replace('isDestructive, err := inspectQuery(queryStr)', 'isDestructive, err := p.queryInspector.IsDestructive(queryStr)')

# Remove inspectQuery func
content = re.sub(r'func inspectQuery\(queryStr string\) \(bool, error\) \{.*?\n\}\n(?=\n// copyWithTimeout)', '', content, flags=re.DOTALL)

with open(proxy_path, "w", encoding="utf-8") as f:
    f.write(content)

# 2. Update main.go
main_path = "main.go"
with open(main_path, "r", encoding="utf-8") as f:
    main_content = f.read()

main_content = main_content.replace('tcpProxy := server.NewTCPProxy(useCase, mode, idleTimeout, rateLimit, jailRepo)', 
    'queryInspector := usecase.NewPGQueryInspector()\n\ttcpProxy := server.NewTCPProxy(useCase, mode, idleTimeout, rateLimit, jailRepo, queryInspector)')

with open(main_path, "w", encoding="utf-8") as f:
    f.write(main_content)

print("Replacement done.")
