package browserx

// Nomes estáveis do produto. A extensão e o host nativo usam o mesmo contrato.
const (
	ServerName     = "hivemind-browser"
	NativeHostName = "com.gilvan_borges.hivemind"
	// ExtensionID sai da chave pública em extension/chrome/manifest.json.
	// A chave privada não está no repositório: o id só fixa o Load unpacked.
	ExtensionID = "jepaepopiknbiklfmjmdagpoppgnhaml"
	DefaultPort = 47891
	serviceName = "hivemind-browser"
)

// Message é o quadro JSON da ponte. Token só existe no hello e não é logado.
type Message struct {
	Type  string         `json:"type"`
	Role  string         `json:"role,omitempty"`
	Token string         `json:"token,omitempty"`
	ID    string         `json:"id,omitempty"`
	Tool  string         `json:"tool,omitempty"`
	Args  map[string]any `json:"args,omitempty"`
	OK    bool           `json:"ok,omitempty"`
	Text  string         `json:"text,omitempty"`
	Code  string         `json:"code,omitempty"`
	Image string         `json:"image,omitempty"`
	Host  string         `json:"host,omitempty"`
}

func (m Message) public() Message {
	m.Token = ""
	return m
}
