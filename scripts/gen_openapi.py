#!/usr/bin/env python3
"""OpenAPI 3 dokümanını uç nokta tablosundan üretir: python3 scripts/gen_openapi.py > internal/api/openapi.yaml"""
import json

E = []  # (method, path, tag, summary, roles, body_schema, status, response_schema, extra_params, idem)
def ep(m, p, tag, summary, roles, body=None, status=200, resp=None, params=None, idem=False):
    E.append(dict(m=m, p=p, tag=tag, s=summary, roles=roles, body=body, status=status, resp=resp, params=params or [], idem=idem))

ALL = ["donor","nurse","lab","bank","hospital","admin"]; STAFF = ["nurse","lab","bank","admin"]
ep("get","/health","Sistem","Sağlık durumu (veritabanı dahil)",[],resp="Health")
ep("get","/metrics","Sistem","Prometheus metrikleri (text/plain)",[])
ep("post","/auth/login","Kimlik","Giriş; access + refresh token",[],body="LoginIn",resp="Tokens")
ep("post","/auth/refresh","Kimlik","Refresh token rotasyonu (eski belirteç iptal edilir)",[],body="RefreshIn",resp="Tokens")
ep("post","/auth/logout","Kimlik","Refresh token iptali",[],body="RefreshIn",status=204)
ep("get","/api/v1/me","Kimlik","Oturumdaki kullanıcı",ALL,resp="User")
ep("get","/api/v1/hospitals","Kimlik","Hastane listesi",ALL)
ep("get","/api/v1/compat","Tahsis","ABO/Rh uyumlu verici grupları (tercih sırasıyla)",STAFF+["hospital"],params=[("component","query","string",False),("group","query","string",True)])
ep("get","/api/v1/live","Pano","Canlı akış: son olaylar ve 5 dakikalık sayaçlar",STAFF)
ep("get","/api/v1/stock","Stok","Bileşen × kan grubu stok matrisi",STAFF,resp="StockRows")
ep("get","/api/v1/me/donor","Bağışçı","Bağışçının kendi profili, uygunluk, geçmiş, randevular",["donor"])
ep("post","/api/v1/me/appointments","Bağışçı","Kendine randevu al",["donor"],body="AppointmentIn",status=201,resp="Appointment",idem=True)
ep("post","/api/v1/me/appointments/{id}/cancel","Bağışçı","Kendi randevusunu iptal et",["donor"],params=[("id","path","integer",True)])
ep("get","/api/v1/donors","Bağışçı kaydı","Bağışçı arama (keyset)",["nurse","bank","admin"],params=[("q","query","string",False),("group","query","string",False),("limit","query","integer",False),("cursor","query","integer",False)],resp="DonorList")
ep("post","/api/v1/donors","Bağışçı kaydı","Bağışçı kaydı (sağlık beyanı şifrelenir)",["nurse","admin"],body="DonorIn",status=201,resp="Donor",idem=True)
ep("get","/api/v1/donors/{id}","Bağışçı kaydı","Bağışçı kimlik bilgisi",["nurse","lab","bank","admin"],params=[("id","path","integer",True)],resp="Donor")
ep("post","/api/v1/donors/{id}/eligibility","Uygunluk","Uygunluk kararı ve gerekçeleri (kayıt oluşturmaz)",["nurse","admin"],body="EligibilityIn",params=[("id","path","integer",True)],resp="Decision")
ep("get","/api/v1/donors/{id}/deferrals","Uygunluk","Ertelemeler",["nurse","bank","admin"],params=[("id","path","integer",True)])
ep("post","/api/v1/donors/{id}/deferrals","Uygunluk","Erteleme ekle (bitiş, başlangıçtaki kural sürümüne göre)",["nurse","admin"],body="DeferralIn",status=201,params=[("id","path","integer",True)],idem=True)
ep("get","/api/v1/donors/{id}/health","Mahremiyet","Şifreli sağlık beyanını oku (X-Access-Reason zorunlu, denetime yazılır)",["nurse","admin"],params=[("id","path","integer",True),("X-Access-Reason","header","string",True)])
ep("get","/api/v1/appointments","Randevu","Güne / bağışçıya göre randevular",["nurse","admin"],params=[("date","query","string",False),("donor_id","query","integer",False)])
ep("post","/api/v1/appointments","Randevu","Randevu ver (randevu gününde uygunluk denetlenir)",["nurse","admin"],body="AppointmentIn",status=201,resp="Appointment",idem=True)
ep("post","/api/v1/appointments/{id}/cancel","Randevu","Randevu iptal",["nurse","admin"],params=[("id","path","integer",True)])
ep("post","/api/v1/donations","Bağış","Bağış kaydı (uygunluk sunucuda yeniden değerlendirilir; uygunsuzsa 422 donor_ineligible)",["nurse","admin"],body="DonationIn",status=201,resp="Donation",idem=True)
ep("get","/api/v1/donations","Bağış","Bağışlar (pending=tests|components, keyset)",["nurse","lab","bank","admin"],params=[("pending","query","string",False),("donor_id","query","integer",False),("limit","query","integer",False),("cursor","query","integer",False)])
ep("get","/api/v1/donations/{id}","Bağış","Tek bağış",["nurse","lab","bank","admin"],params=[("id","path","integer",True)],resp="Donation")
ep("post","/api/v1/donations/{id}/components","Üretim","Bileşen üretimi (RBC/PLT/FFP)",["lab","admin"],body="ComponentsIn",status=201,params=[("id","path","integer",True)],idem=True)
ep("post","/api/v1/donations/{id}/test-results","Test","İlk test sonucu; pozitifse tüm türevler (recursive) karantina + look-back",["lab"],body="Markers",status=201,params=[("id","path","integer",True)],resp="TestOutcome",idem=True)
ep("post","/api/v1/donations/{id}/test-changes","Test","Test sonucu değişiklik talebi (1. onay)",["lab"],body="TestChangeIn",status=201,params=[("id","path","integer",True)],idem=True)
ep("get","/api/v1/test-changes","Test","Değişiklik talepleri",["lab","admin"],params=[("status","query","string",False)])
ep("post","/api/v1/test-changes/{id}/approve","Test","2. onay (talebi açan onaylayamaz → 403 self_approval_forbidden)",["lab"],params=[("id","path","integer",True)])
ep("post","/api/v1/test-changes/{id}/reject","Test","Talebi reddet",["lab"],params=[("id","path","integer",True)])
ep("get","/api/v1/products","Stok","Ürünler (status, component, group filtreleri; sort=expiry FEFO; keyset)",["lab","bank","admin"],params=[("status","query","string",False),("component","query","string",False),("group","query","string",False),("sort","query","string",False),("limit","query","integer",False),("cursor","query","integer",False)])
ep("get","/api/v1/products/{id}","Stok","Ürün ve geri alınamaz durum geçmişi",["lab","bank","admin"],params=[("id","path","integer",True)])
ep("post","/api/v1/products/{id}/split","Stok","Ürünü alt ürünlere böl (parent_id ile bağlı)",["bank"],body="SplitIn",status=201,params=[("id","path","integer",True)],idem=True)
ep("post","/api/v1/products/{id}/discard","Stok","İmha (gerekçe zorunlu)",["lab","bank"],body="ReasonIn",params=[("id","path","integer",True)])
ep("post","/api/v1/products/{id}/transition","Stok","Doğrudan durum geçişi (geçersizler 409 invalid_transition)",["bank","admin"],body="TransitionIn",params=[("id","path","integer",True)])
ep("get","/api/v1/products/verify","Stok","İmzalı barkod doğrulama",STAFF+["hospital"],params=[("code","query","string",True)])
ep("post","/api/v1/requests","Talep","Hastane talebi; anında tahsis denenir",["hospital"],body="RequestIn",status=201,resp="Request",idem=True)
ep("get","/api/v1/requests","Talep","Talepler (hastane yalnızca kendi taleplerini görür; keyset)",["hospital","bank","admin"],params=[("status","query","string",False),("limit","query","integer",False),("cursor","query","integer",False)])
ep("get","/api/v1/requests/{id}","Talep","Talep ve rezervasyonlar (bağışçı bilgisi içermez)",["hospital","bank","admin"],params=[("id","path","integer",True)],resp="Request")
ep("post","/api/v1/requests/{id}/cancel","Talep","Talebi iptal et",["hospital","bank"],params=[("id","path","integer",True)])
ep("post","/api/v1/requests/{id}/allocate","Tahsis","Tahsis (uyum + FEFO, SKIP LOCKED; acil talep rutin rezervasyonu devralabilir)",["bank"],body="AllocateIn",params=[("id","path","integer",True)],idem=True)
ep("post","/api/v1/requests/{id}/issue","Dağıtım","Aktif rezervasyonları dağıt",["bank"],params=[("id","path","integer",True)],resp="Request",idem=True)
ep("get","/api/v1/reservations","Tahsis","Aktif rezervasyonlar",["bank","admin"])
ep("get","/api/v1/reservations/{id}","Tahsis","Rezervasyon durumu",["bank","admin"],params=[("id","path","integer",True)])
ep("post","/api/v1/reservations/{id}/release","Tahsis","Rezervasyonu bırak",["bank"],params=[("id","path","integer",True)])
ep("get","/api/v1/hospital/products","Dağıtım","Hastaneye dağıtılan ürünler",["hospital"])
ep("post","/api/v1/transfusions","Dağıtım","Transfüzyon kaydı",["hospital"],body="TransfusionIn",status=201,idem=True)
ep("get","/api/v1/trace/donors/{id}","İzleme","Bağışçı → hasta (recursive)",["bank","admin"],params=[("id","path","integer",True)])
ep("get","/api/v1/trace/patients/{ref}","İzleme","Hasta → bağışçı (look-back, recursive)",["bank","admin"],params=[("ref","path","string",True)])
ep("get","/api/v1/forecast","Tahmin","Grup bazında tüketim tahmini, yeterlilik ve SKT kaybı",["bank","admin"],params=[("component","query","string",False),("horizon","query","integer",False)])
ep("get","/api/v1/forecast/backtest","Tahmin","Geriye dönük test (MAPE, WAPE)",["bank","admin"],params=[("component","query","string",False),("days","query","integer",False)])
ep("get","/api/v1/alerts","Tahmin","Stok alarmları",["bank","admin"],params=[("all","query","string",False)])
ep("post","/api/v1/alerts/evaluate","Tahmin","Alarmları hemen değerlendir",["bank","admin"])
ep("get","/api/v1/call-list","Tahmin","Kritik grup için uygun bağışçı çağrı listesi",["bank","admin"],params=[("group","query","string",False),("limit","query","integer",False)])
ep("get","/api/v1/reports/summary","Rapor","Dönem raporu",["bank","admin"],params=[("from","query","string",False),("to","query","string",False)])
ep("get","/api/v1/users","Yönetim","Kullanıcılar",["admin"])
ep("post","/api/v1/users","Yönetim","Kullanıcı ekle",["admin"],body="UserIn",status=201,resp="User",idem=True)
ep("get","/api/v1/rules","Yönetim","Kural sürümleri",["admin","nurse","bank"],params=[("code","query","string",False)])
ep("post","/api/v1/rules","Yönetim","İleri tarihli kural sürümü ekle",["admin"],body="RuleIn",status=201,idem=True)
ep("get","/api/v1/audit","Yönetim","Denetim kaydı (keyset)",["admin"],params=[("action","query","string",False),("entity","query","string",False),("entity_id","query","string",False),("limit","query","integer",False),("cursor","query","integer",False)])

def obj(props, req=None):
    o = {"type":"object","properties":props}
    if req: o["required"]=req
    return o
S = lambda t, **k: {"type":t, **k}
schemas = {
 "Error": obj({"error": obj({"code":S("string"),"message":S("string"),"details":{}}, ["code","message"])}, ["error"]),
 "Health": obj({"status":S("string"),"checks":S("object")}),
 "LoginIn": obj({"email":S("string",format="email"),"password":S("string")},["email","password"]),
 "RefreshIn": obj({"refresh_token":S("string")},["refresh_token"]),
 "User": obj({"id":S("integer"),"email":S("string"),"role":S("string",enum=ALL),"full_name":S("string"),"hospital_id":S("integer",nullable=True),"donor_id":S("integer",nullable=True)}),
 "Tokens": obj({"access_token":S("string"),"refresh_token":S("string"),"expires_at":S("string",format="date-time"),"user":{"$ref":"#/components/schemas/User"}}),
 "HealthInfo": obj({"medications":S("array",items=S("string")),"conditions":S("array",items=S("string")),"notes":S("string")}),
 "DonorIn": obj({"first_name":S("string"),"last_name":S("string"),"sex":S("string",enum=["M","F"]),"birth_date":S("string",format="date"),"abo":S("string",enum=["O","A","B","AB"]),"rh_positive":S("boolean"),"phone":S("string"),"email":S("string"),"health":{"$ref":"#/components/schemas/HealthInfo"}},["first_name","last_name","sex","birth_date","abo","rh_positive"]),
 "Donor": obj({"id":S("integer"),"donor_no":S("string"),"first_name":S("string"),"last_name":S("string"),"sex":S("string"),"birth_date":S("string",format="date"),"group":S("string"),"phone":S("string")}),
 "DonorList": obj({"items":S("array",items={"$ref":"#/components/schemas/Donor"}),"next_cursor":S("integer",nullable=True)}),
 "EligibilityIn": obj({"donation_type":S("string",enum=["whole_blood","platelet_apheresis"]),"at":S("string",format="date"),"measurements":obj({"hemoglobin":S("number"),"weight_kg":S("number")}),"declarations":S("array",items=obj({"code":S("string"),"date":S("string",format="date")}))}),
 "Decision": obj({"eligible":S("boolean"),"reasons":S("array",items=obj({"code":S("string"),"message":S("string"),"until":S("string",format="date-time"),"permanent":S("boolean")})),"next_eligible_at":S("string",format="date-time",nullable=True),"rule_version":S("string")}),
 "DeferralIn": obj({"reason_code":S("string"),"starts_on":S("string",format="date"),"note":S("string")},["reason_code"]),
 "AppointmentIn": obj({"donor_id":S("integer"),"scheduled_at":S("string",format="date-time"),"donation_type":S("string")},["scheduled_at"]),
 "Appointment": obj({"id":S("integer"),"donor_id":S("integer"),"scheduled_at":S("string",format="date-time"),"status":S("string")}),
 "DonationIn": obj({"donor_id":S("integer"),"appointment_id":S("integer"),"donation_type":S("string"),"volume_ml":S("integer"),"hemoglobin":S("number"),"weight_kg":S("number"),"declarations":S("array",items=S("object")),"collected_at":S("string",format="date-time")},["donor_id","volume_ml","hemoglobin","weight_kg"]),
 "Donation": obj({"id":S("integer"),"donor_id":S("integer"),"donation_type":S("string"),"collected_at":S("string",format="date-time"),"bag_no":S("string"),"test_outcome":S("string",nullable=True),"products":S("integer")}),
 "ComponentsIn": obj({"components":S("array",items=S("string",enum=["RBC","PLT","FFP"]))}),
 "Markers": obj({"hiv":S("boolean"),"hbv":S("boolean"),"hcv":S("boolean"),"syphilis":S("boolean")},["hiv","hbv","hcv","syphilis"]),
 "TestOutcome": obj({"donation_id":S("integer"),"outcome":S("string"),"released":S("integer"),"quarantined":S("array",items=S("object")),"recalls":S("array",items=S("object")),"lookback_donations":S("array",items=S("integer"))}),
 "TestChangeIn": obj({"proposed":{"$ref":"#/components/schemas/Markers"},"reason":S("string")},["proposed","reason"]),
 "SplitIn": obj({"parts":S("integer",minimum=2,maximum=4)},["parts"]),
 "ReasonIn": obj({"reason":S("string")},["reason"]),
 "TransitionIn": obj({"to":S("string"),"reason":S("string")},["to","reason"]),
 "StockRows": S("array",items=obj({"component":S("string"),"group":S("string"),"available":S("integer"),"reserved":S("integer"),"pending_test":S("integer"),"expiring_48h":S("integer"),"alert":S("string",nullable=True)})),
 "RequestIn": obj({"component":S("string",enum=["RBC","PLT","FFP"]),"group":S("string",example="A+"),"units":S("integer",minimum=1,maximum=20),"urgency":S("string",enum=["routine","urgent","emergency"]),"patient_ref":S("string"),"needed_by":S("string",format="date-time")},["component","group","units","patient_ref"]),
 "Request": obj({"id":S("integer"),"hospital_name":S("string"),"component":S("string"),"group":S("string"),"units":S("integer"),"urgency":S("string"),"patient_ref":S("string"),"status":S("string"),"reservations":S("array",items=obj({"id":S("integer"),"product_no":S("string"),"group":S("string"),"product_expires_at":S("string",format="date-time"),"status":S("string"),"hold_until":S("string",format="date-time"),"exact_match":S("boolean")}))}),
 "AllocateIn": obj({"ttl_seconds":S("integer")}),
 "TransfusionIn": obj({"product_no":S("string"),"patient_ref":S("string")},["product_no","patient_ref"]),
 "UserIn": obj({"email":S("string"),"password":S("string"),"role":S("string"),"full_name":S("string"),"hospital_id":S("integer"),"donor_id":S("integer")},["email","password","role","full_name"]),
 "RuleIn": obj({"code":S("string",enum=["eligibility","shelf_life"]),"valid_from":S("string",format="date"),"note":S("string"),"params":S("object")},["code","valid_from","params"]),
}

def key(k):
    k = str(k)
    if k.isdigit() or not k.replace('_','').replace('-','').isalnum():
        return json.dumps(k, ensure_ascii=False)
    return k

def y(v, ind=0):
    """Minimal YAML yazıcı (JSON alt kümesi)."""
    sp = "  "*ind
    if isinstance(v, dict):
        if not v: return "{}"
        out=[]
        for k,val in v.items():
            if isinstance(val,(dict,list)) and val:
                out.append(f"{sp}{key(k)}:\n{y(val,ind+1)}")
            else:
                out.append(f"{sp}{key(k)}: {y(val,0)}")
        return "\n".join(out)
    if isinstance(v, list):
        if not v: return "[]"
        out=[]
        for it in v:
            if isinstance(it,(dict,list)) and it:
                s=y(it,ind+1).lstrip()
                out.append(f"{sp}- {s}")
            else:
                out.append(f"{sp}- {y(it,0)}")
        return "\n".join(out)
    return json.dumps(v, ensure_ascii=False)

paths={}
for e in E:
    op={"tags":[e["tag"]],"summary":e["s"],"operationId":(e["m"]+e["p"]).replace("/","_").replace("{","").replace("}","").replace("-","_")}
    desc=[]
    if e["roles"]:
        desc.append("Roller: "+", ".join(e["roles"]))
    else:
        op["security"]=[]
    if e["idem"]: desc.append("Idempotency-Key başlığını destekler.")
    if desc: op["description"]=" ".join(desc)
    params=[{"name":n,"in":i,"required":r,"schema":{"type":t}} for (n,i,t,r) in e["params"]]
    if e["idem"]: params.append({"name":"Idempotency-Key","in":"header","required":False,"schema":{"type":"string","minLength":8,"maxLength":128}})
    if params: op["parameters"]=params
    if e["body"]:
        op["requestBody"]={"required":True,"content":{"application/json":{"schema":{"$ref":f"#/components/schemas/{e['body']}"}}}}
    ok={"description":"Başarılı"}
    if e["resp"]: ok["content"]={"application/json":{"schema":{"$ref":f"#/components/schemas/{e['resp']}"}}}
    if e["p"]=="/metrics": ok["content"]={"text/plain":{"schema":{"type":"string"}}}
    resps={str(e["status"]):ok}
    err={"content":{"application/json":{"schema":{"$ref":"#/components/schemas/Error"}}}}
    if e["roles"]:
        resps["401"]={"description":"Kimlik doğrulama gerekli",**err}; resps["403"]={"description":"Rol yetkisiz",**err}
    if e["body"] or e["params"]: resps["422"]={"description":"Doğrulama hatası",**err}
    if e["m"]=="post" and e["roles"]: resps["409"]={"description":"Çakışma / geçersiz durum geçişi",**err}
    op["responses"]=resps
    paths.setdefault(e["p"],{})[e["m"]]=op

doc={"openapi":"3.0.3","info":{"title":"DamarBağış API","version":"1.0.0","description":"Kan bağış merkezi ve kan bankası stok sistemi. Tüm hatalar {\"error\":{\"code\",\"message\",\"details\"}} biçimindedir."},
     "servers":[{"url":"http://localhost:8080"}],
     "security":[{"bearer":[]}],
     "paths":paths,
     "components":{"securitySchemes":{"bearer":{"type":"http","scheme":"bearer","bearerFormat":"JWT"}},"schemas":schemas}}
print("# Bu dosya scripts/gen_openapi.py ile üretilir.")
print(y(doc))
