package client

type ExchangeResp struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TenantID    string `json:"tenant_id"`
}

type RulesResolveResp struct {
	UpToDate        bool   `json:"up_to_date"`
	RulesVersion    string `json:"rules_version"`
	ManifestSHA     string `json:"manifest_sha256"`
	DownloadURL     string `json:"download_url"`
	SignatureBase64 string `json:"signature_base64,omitempty"`
	SignatureURL    string `json:"signature_url,omitempty"`
	SignatureAlgo   string `json:"signature_algo,omitempty"`
}

type GenerateReq struct {
	InputMarkdown  string `json:"input_markdown"`
	CandidateCount int    `json:"candidate_count,omitempty"`
}

type GenerateResp struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
}

type JobStatusResp struct {
	JobID     string `json:"job_id"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type ResultResp struct {
	ENMarkdown       string   `json:"en_markdown"`
	CNMarkdown       string   `json:"cn_markdown"`
	ValidationReport []string `json:"validation_report"`
	TimingMS         int64    `json:"timing_ms"`
}
