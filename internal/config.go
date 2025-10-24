package internal

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
)

// Config representa a configuração completa do rotom-worker,
// espelhando exatamente a estrutura do seu rotom-config.json.
type Config struct {
	Rotom struct {
		WorkerEndpoint string `json:"worker_endpoint"`
		DeviceEndpoint string `json:"device_endpoint"`
		Secret         string `json:"secret"`
		UseCompression bool   `json:"use_compression"`
	} `json:"rotom"`

	General struct {
		DeviceName string `json:"device_name"`
		Workers    int    `json:"workers"`
		DnsServer  string `json:"dns_server"`
		ScanDir    string `json:"scan_dir"`
	} `json:"general"`

	Log struct {
		Level     string `json:"level"`
		UseColors bool   `json:"use_colors"`
		LogToFile bool   `json:"log_to_file"`
		MaxSize   int    `json:"max_size"`
		MaxBackups int   `json:"max_backups"`
		MaxAge    int    `json:"max_age"`
		Compress  bool   `json:"compress"`
		FilePath  string `json:"file_path"`
	} `json:"log"`

	Tuning struct {
		WorkerSpawnDelayMs int `json:"worker_spawn_delay_ms"`
	} `json:"tuning"`
}

// defaultConfig preenche valores seguros caso falhe a leitura do arquivo.
func defaultConfig() Config {
	var c Config
	c.Rotom.WorkerEndpoint = "ws://127.0.0.1:9001"
	c.Rotom.DeviceEndpoint = ""
	c.Rotom.Secret = ""
	c.Rotom.UseCompression = false

	c.General.DeviceName = "android-device"
	c.General.Workers = 1
	c.General.DnsServer = "1.1.1.1:53"
	c.General.ScanDir = "/data/local/tmp/rotom_inbox"

	c.Log.Level = "info"
	c.Log.UseColors = true
	c.Log.LogToFile = false
	c.Log.MaxSize = 10
	c.Log.MaxBackups = 5
	c.Log.MaxAge = 7
	c.Log.Compress = false
	c.Log.FilePath = "/data/local/tmp/rotom-worker.log"

	c.Tuning.WorkerSpawnDelayMs = 500
	return c
}

// ReadConfig lê o arquivo JSON em path. Em caso de erro, retorna defaults e imprime aviso.
func ReadConfig(path string) Config {
	cfg := defaultConfig()

	if path == "" {
		return cfg
	}

	b, err := ioutil.ReadFile(path)
	if err != nil {
		// não aborta; apenas informa e retorna defaults
		fmt.Fprintf(os.Stderr, "[config] warning: cannot read %s: %v — usando defaults\n", path, err)
		return cfg
	}

	if err := json.Unmarshal(b, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "[config] parse error %s: %v — usando defaults\n", path, err)
		return cfg
	}

	// pós-processamento / saneamento básico
	cfg.sanitize()
	return cfg
}

// sanitize aplica correções simples (por exemplo endpoints vazios)
func (c *Config) sanitize() {
	// trim espaços
	c.Rotom.WorkerEndpoint = strings.TrimSpace(c.Rotom.WorkerEndpoint)
	c.Rotom.DeviceEndpoint = strings.TrimSpace(c.Rotom.DeviceEndpoint)
	c.General.DeviceName = strings.TrimSpace(c.General.DeviceName)

	// se DeviceEndpoint estiver vazio, tente derivar a partir de WorkerEndpoint
	if c.Rotom.DeviceEndpoint == "" && c.Rotom.WorkerEndpoint != "" {
		// se WorkerEndpoint termina com /control ou /data, deixe como está; caso contrário, append "/control"
		if strings.HasSuffix(c.Rotom.WorkerEndpoint, "/control") || strings.HasSuffix(c.Rotom.WorkerEndpoint, "/data") {
			c.Rotom.DeviceEndpoint = c.Rotom.WorkerEndpoint
		} else {
			// padrão: o usuário forneceu o IP:PORT base (ex: ws://ip:port), DeviceEndpoint = base + "/control"
			c.Rotom.DeviceEndpoint = strings.TrimRight(c.Rotom.WorkerEndpoint, "/") + "/control"
		}
	}

	// garantir valores mínimos válidos
	if c.General.Workers < 1 {
		c.General.Workers = 1
	}
	if c.Tuning.WorkerSpawnDelayMs <= 0 {
		c.Tuning.WorkerSpawnDelayMs = 500
	}
	if c.Log.MaxSize <= 0 {
		c.Log.MaxSize = 10
	}
	if c.Log.MaxBackups < 0 {
		c.Log.MaxBackups = 0
	}
	if c.Log.MaxAge <= 0 {
		c.Log.MaxAge = 7
	}
	if c.Log.FilePath == "" {
		c.Log.FilePath = "/data/local/tmp/rotom-worker.log"
	}
}

// Helper: retorna endpoint de dados (/data) baseado na configuração
func (c *Config) DataEndpoint() string {
	base := strings.TrimRight(c.Rotom.WorkerEndpoint, "/")
	// se worker_endpoint já apontar explicitamente para /data use ele
	if strings.HasSuffix(base, "/data") || strings.HasSuffix(c.Rotom.DeviceEndpoint, "/data") {
		// preferir DeviceEndpoint se setado
		if c.Rotom.DeviceEndpoint != "" {
			return c.Rotom.DeviceEndpoint
		}
		return base
	}
	// padrão: worker_endpoint + "/data"
	return base + "/data"
}

// Helper: retorna endpoint de controle (/control). Prefere DeviceEndpoint se informado.
func (c *Config) ControlEndpoint() string {
	if c.Rotom.DeviceEndpoint != "" {
		return c.Rotom.DeviceEndpoint
	}
	base := strings.TrimRight(c.Rotom.WorkerEndpoint, "/")
	return base + "/control"
}
