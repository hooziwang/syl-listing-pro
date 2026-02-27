package client

type ExchangeResp struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TenantID    string `json:"tenant_id"`
}

type RulesResolveResp struct {
	UpToDate                        bool   `json:"up_to_date"`
	RulesVersion                    string `json:"rules_version"`
	ManifestSHA                     string `json:"manifest_sha256"`
	DownloadURL                     string `json:"download_url"`
	SignatureBase64                 string `json:"signature_base64,omitempty"`
	SignatureURL                    string `json:"signature_url,omitempty"`
	SignatureAlgo                   string `json:"signature_algo,omitempty"`
	SigningPublicKeyPathInArchive   string `json:"signing_public_key_path_in_archive,omitempty"`
	SigningPublicKeySignatureBase64 string `json:"signing_public_key_signature_base64,omitempty"`
	SigningPublicKeySignatureAlgo   string `json:"signing_public_key_signature_algo,omitempty"`
}

type GenerateReq struct {
	InputMarkdown  string `json:"input_markdown"`
	InputFilename  string `json:"input_filename,omitempty"`
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

type CancelResp struct {
	OK        bool   `json:"ok"`
	JobID     string `json:"job_id"`
	Status    string `json:"status"`
	Cancelled bool   `json:"cancelled"`
	Reason    string `json:"reason,omitempty"`
}

type ResultResp struct {
	ENMarkdown       string   `json:"en_markdown"`
	CNMarkdown       string   `json:"cn_markdown"`
	ValidationReport []string `json:"validation_report"`
	TimingMS         int64    `json:"timing_ms"`
	Meta             struct {
		HighlightWordsEN []string `json:"highlight_words_en"`
		HighlightWordsCN []string `json:"highlight_words_cn"`
	} `json:"meta"`
}

type JobTraceItem struct {
	TS        string         `json:"ts"`
	Source    string         `json:"source"`
	Event     string         `json:"event"`
	Level     string         `json:"level,omitempty"`
	TenantID  string         `json:"tenant_id"`
	JobID     string         `json:"job_id"`
	ElapsedMS int64          `json:"elapsed_ms"`
	ReqID     string         `json:"req_id,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
}

type JobTraceResp struct {
	OK         bool           `json:"ok"`
	JobID      string         `json:"job_id"`
	JobStatus  string         `json:"job_status"`
	TenantID   string         `json:"tenant_id"`
	TraceCount int            `json:"trace_count"`
	Limit      int            `json:"limit"`
	Offset     int            `json:"offset"`
	NextOffset int            `json:"next_offset"`
	HasMore    bool           `json:"has_more"`
	Items      []JobTraceItem `json:"items"`
}
