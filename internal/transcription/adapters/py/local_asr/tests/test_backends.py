"""Exercise published backend call contracts with lightweight library doubles.

These verify argument wiring, precision, token filtering, parsing, and output
failure handling. They do not measure recognizer quality or CPU performance.
"""
import contextlib
from pathlib import Path
import sys
import types

import numpy as np
import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from backends import RecognitionError, TransformersBackend, prepare_moss_preview_inputs, to_device


class Tensor:
    def __init__(self, floating):
        self.floating=floating
        self.dtype="float64" if floating else "int64"
        self.moves=[]
    def is_floating_point(self): return self.floating
    def to(self,**kwargs): self.moves.append(kwargs);return self


def test_audio_precision_changes_without_casting_token_ids(monkeypatch):
    monkeypatch.setitem(sys.modules,"torch",types.SimpleNamespace(is_tensor=lambda obj:isinstance(obj,Tensor)))
    audio,ids=Tensor(True),Tensor(False)
    to_device({"input_features":audio,"input_ids":ids},"cpu","float32")
    assert audio.moves == [{"device":"cpu","dtype":"float32"}]
    assert ids.moves == [{"device":"cpu","dtype":"int64"}]


@pytest.fixture
def libraries(monkeypatch):
    calls=[]
    tokenizer=types.SimpleNamespace(
        eos_token_id=99,pad_token_id=0,all_special_ids=[0,1,99],
        get_added_vocab=lambda:{"<noise>":77,"ordinary":88},
        apply_chat_template=lambda messages,**kwargs:(calls.append(("template",messages,kwargs)) or "rendered-prompt"),
        decode=lambda *args,**kwargs:decoded[0],
    )
    decoded=["hello"]
    class Processor:
        end_token_id=99
        def __init__(self,*args,**kwargs): self.tokenizer=tokenizer
        def __call__(self,*args,**kwargs):
            calls.append(("processor",args,kwargs));return {"input_ids":np.array([[1,2]])}
        def apply_chat_template(self,messages,**kwargs):
            calls.append(("processor_template",messages,kwargs))
            return "rendered-prompt" if kwargs.get("tokenize") is False else {"input_ids":np.array([[1,2]])}
        def decode(self,*args,**kwargs): return decoded[0]
        def batch_decode(self,*args,**kwargs): return [decoded[0]]
        def load_template(self,path): calls.append(("load_template",path))
    processor=Processor()
    class Model:
        generation_config=types.SimpleNamespace(eos_token_id=99)
        config=types.SimpleNamespace(max_seq_len=1024)
        def to(self,device): calls.append(("model_to",device));return self
        def eval(self): return self
        def generate(self,**kwargs):
            calls.append(("generate",kwargs));return np.array([[1,2,10,11,99]])
    model=Model()
    class Loader:
        _tied_weights_keys=["lm_head.weight"]
        @staticmethod
        def from_pretrained(model_id,**kwargs):
            if model_id == "OpenMOSS-Team/MOSS-Transcribe-preview-2B":
                assert Loader._tied_weights_keys == {"lm_head.weight":"model.language_model.embed_tokens.weight"}
                assert kwargs["tie_word_embeddings"] is True
                assert Loader.prepare_inputs_for_generation is prepare_moss_preview_inputs
            calls.append(("model_load",model_id,kwargs));return model
    class ProcessorLoader:
        @staticmethod
        def from_pretrained(model_id,**kwargs):
            calls.append(("processor_load",model_id,kwargs));return processor
    transformers=types.SimpleNamespace(AutoModelForCausalLM=Loader,AutoModelForCTC=Loader,AutoModelForSpeechSeq2Seq=Loader,CohereAsrForConditionalGeneration=Loader,AutoProcessor=ProcessorLoader,AutoTokenizer=types.SimpleNamespace(from_pretrained=lambda *args,**kwargs:tokenizer))
    monkeypatch.setitem(sys.modules,"transformers",transformers)
    torch=types.SimpleNamespace(inference_mode=contextlib.nullcontext,is_tensor=lambda value:False)
    monkeypatch.setitem(sys.modules,"torch",torch)
    def dynamic_class(name,model_id,**kwargs):
        calls.append(("dynamic",name,model_id,kwargs))
        if name.endswith("MossForCausalLM"):
            return Loader
        return Processor if name.endswith("MossProcessor") else (lambda **kwargs:kwargs)
    monkeypatch.setitem(sys.modules,"transformers.dynamic_module_utils",types.SimpleNamespace(get_class_from_dynamic_module=dynamic_class))
    monkeypatch.setitem(sys.modules,"huggingface_hub",types.SimpleNamespace(hf_hub_download=lambda *args,**kwargs:"/cached/template.py"))
    return calls,decoded


def test_moss_preview_audio_is_prefill_only_with_modern_cache_api(monkeypatch):
    calls = []
    class GenerationMixin:
        def prepare_inputs_for_generation(self, input_ids, next_sequence_length=None, **kwargs):
            calls.append((input_ids, next_sequence_length, kwargs))
            return {"input_ids": input_ids[:, -next_sequence_length:] if next_sequence_length else input_ids, **kwargs}
    monkeypatch.setitem(sys.modules, "transformers.generation.utils", types.SimpleNamespace(GenerationMixin=GenerationMixin))
    model, cache = object(), object()
    audio_kwargs = {"audio_data": object(), "audio_data_seqlens": object(), "audio_input_mask": np.ones((1, 205)), "use_cache": True}
    prefill = prepare_moss_preview_inputs(model, np.zeros((1, 205)), next_sequence_length=205, past_key_values=cache, is_first_iteration=True, **audio_kwargs)
    decode = prepare_moss_preview_inputs(model, np.zeros((1, 206)), next_sequence_length=1, past_key_values=cache, is_first_iteration=False, **audio_kwargs)
    assert prefill["input_ids"].shape == (1, 205)
    assert prefill["audio_input_mask"].shape == (1, 205)
    assert decode["input_ids"].shape == (1, 1)
    assert decode["past_key_values"] is cache
    assert not any(key in decode for key in ("audio_data", "audio_data_seqlens", "audio_input_mask"))
    assert calls[1][1] == 1 and calls[1][2]["is_first_iteration"] is False


@pytest.mark.parametrize("engine",["cohere","granite","granite_plus","granite_ctc","ark","moss","moss_preview"])
def test_publisher_backend_contracts(libraries,engine):
    calls,decoded=libraries
    cfg={"engine":engine,"revision":"approved-sha","language":"en","context":"","context_terms":""}
    model_id="publisher/model"
    if engine == "moss_preview":
        model_id="OpenMOSS-Team/MOSS-Transcribe-preview-2B"
        cfg["revision"]="c98175cb20e48bd9be4e95f6c85f2af18899f780"
    if engine in {"granite","granite_plus","moss"}:
        cfg["context_terms"]="PostgreSQL\nScriberr"
    if engine=="granite_plus": decoded[0]="hello [T:80]"
    if engine=="moss":
        cfg["context"]="release meeting"
        decoded[0]="[0.25][S02]hello[0.8]"
    backend=TransformersBackend(model_id,"cpu","float32",cfg)
    result=backend.transcribe(np.ones(16000,dtype=np.float32))
    assert result["text"]=="hello"
    load=next(c for c in calls if c[0]=="model_load")
    assert load[2]["dtype"]=="float32"
    assert ("model_to","cpu") in calls
    if engine in {"ark","moss","moss_preview"}:
        assert load[2]["trust_remote_code"] is True
        assert load[2]["revision"]==cfg["revision"]
    else:
        assert not load[2].get("trust_remote_code",False)
    if engine=="ark":
        template=next(c for c in calls if c[0]=="processor_template")
        assert template[1][0]["content"][0]["type"]=="audio"
        assert isinstance(template[1][0]["content"][0]["array"],np.ndarray)
        generation=next(c for c in calls if c[0]=="generate")[1]
        assert [77] in generation["bad_words_ids"] and [99] not in generation["bad_words_ids"]
    if engine in {"granite","granite_plus"}:
        prompt=next(c for c in calls if c[0]=="template")[1][-1]["content"]
        assert "Keywords: PostgreSQL, Scriberr" in prompt
    if engine=="granite_plus":
        assert result["word_segments"]==[{"word":"hello","start":0.0,"end":0.8}]
    if engine=="moss":
        prompt=next(c for c in calls if c[0]=="processor_template")[1][0]["content"][1]["text"]
        assert "PostgreSQL, Scriberr" in prompt and "release meeting" in prompt
        assert result["segments"][0]["speaker"]=="S02"


def test_cohere_auto_reuses_successful_retry_budget_for_later_windows(libraries):
    backend=TransformersBackend("publisher/model","cpu","float32",{"engine":"cohere","language":"en"})
    budgets=[]
    def generate(**kwargs):
        budgets.append(kwargs["max_new_tokens"])
        if len(budgets)==1:
            return np.full((1,kwargs["max_new_tokens"]),10)
        return np.array([[10,99]])
    backend.model.generate=generate
    assert backend.transcribe(np.ones(30*16000,dtype=np.float32))["text"]=="hello"
    assert backend.transcribe(np.ones(30*16000,dtype=np.float32))["text"]=="hello"
    assert budgets==[768,1000,1000]


def test_cohere_auto_still_rejects_incomplete_retry(libraries):
    backend=TransformersBackend("publisher/model","cpu","float32",{"engine":"cohere","language":"en"})
    budgets=[]
    def generate(**kwargs):
        budgets.append(kwargs["max_new_tokens"])
        return np.full((1,kwargs["max_new_tokens"]),10)
    backend.model.generate=generate
    with pytest.raises(RecognitionError,match="Auto output limit") as failure:
        backend.transcribe(np.ones(30*16000,dtype=np.float32))
    assert failure.value.token_limit==1000
    assert budgets==[768,1000]


def test_cohere_auto_respects_loaded_decoder_length(libraries):
    backend=TransformersBackend("publisher/model","cpu","float32",{"engine":"cohere","language":"en"})
    backend.model.config.max_seq_len=900
    budgets=[]
    def generate(**kwargs):
        budgets.append(kwargs["max_new_tokens"])
        return np.full((1,kwargs["max_new_tokens"]),10) if len(budgets)==1 else np.array([[10,99]])
    backend.model.generate=generate
    assert backend.transcribe(np.ones(30*16000,dtype=np.float32))["text"]=="hello"
    assert backend.transcribe(np.ones(30*16000,dtype=np.float32))["text"]=="hello"
    assert budgets==[768,876,876]


def test_cohere_explicit_budget_is_exact_and_never_retried(libraries):
    backend=TransformersBackend("publisher/model","cpu","float32",{"engine":"cohere","language":"en","max_new_tokens":400})
    budgets=[]
    def generate(**kwargs):
        budgets.append(kwargs["max_new_tokens"])
        return np.full((1,kwargs["max_new_tokens"]),10)
    backend.model.generate=generate
    with pytest.raises(RecognitionError,match="token limit") as failure:
        backend.transcribe(np.ones(30*16000,dtype=np.float32))
    assert failure.value.token_limit==400
    assert budgets==[400]
