-- 0003: başlangıç referans verisi (hastaneler, kural sürümleri)

INSERT INTO hospitals (name, city) VALUES
 ('Bandırma Devlet Hastanesi', 'Balıkesir'),
 ('Balıkesir Atatürk Şehir Hastanesi', 'Balıkesir'),
 ('Bursa Şehir Hastanesi', 'Bursa'),
 ('Çanakkale Eğitim ve Araştırma Hastanesi', 'Çanakkale'),
 ('Erdek Devlet Hastanesi', 'Balıkesir');

INSERT INTO rule_versions (code, valid_from, note, params) VALUES
('eligibility', DATE '2020-01-01', 'İlk kural seti', $json$
{
  "min_age": 18,
  "max_age": 65,
  "min_weight_kg": 50,
  "hb_min": {"M": 13.5, "F": 12.5},
  "hb_max": 18.5,
  "intervals_days": {
    "whole_blood": {
      "whole_blood":        {"M": 90, "F": 120},
      "platelet_apheresis": {"M": 28, "F": 28}
    },
    "platelet_apheresis": {
      "whole_blood":        {"M": 14, "F": 14},
      "platelet_apheresis": {"M": 14, "F": 14}
    }
  },
  "max_platelet_per_365": 24,
  "deferrals": {
    "travel_malaria":    {"days": 365, "label": "Sıtma riskli bölgeye seyahat"},
    "tattoo_piercing":   {"days": 180, "label": "Dövme / piercing"},
    "surgery_major":     {"days": 180, "label": "Büyük cerrahi operasyon"},
    "antibiotic":        {"days": 14,  "label": "Antibiyotik kullanımı"},
    "isotretinoin":      {"days": 30,  "label": "İzotretinoin kullanımı"},
    "dental":            {"days": 7,   "label": "Diş tedavisi"},
    "aspirin":           {"days": 5,   "label": "Aspirin kullanımı", "only": "platelet_apheresis"},
    "hiv_positive":      {"permanent": true, "label": "HIV pozitif test"},
    "hbv_positive":      {"permanent": true, "label": "Hepatit B pozitif test"},
    "hcv_positive":      {"permanent": true, "label": "Hepatit C pozitif test"},
    "syphilis_positive": {"permanent": true, "label": "Sifiliz pozitif test"},
    "iv_drug_use":       {"permanent": true, "label": "Damar içi madde kullanımı öyküsü"}
  }
}
$json$),
('shelf_life', DATE '2020-01-01', 'Ürün raf ömürleri ve izleme penceresi', $json$
{
  "days": {"RBC": 42, "PLT": 5, "FFP": 365},
  "lookback_days": 365,
  "reservation_ttl_minutes": 30
}
$json$);
