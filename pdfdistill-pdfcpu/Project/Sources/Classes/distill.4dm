property onData : 4D:C1709.Function
property onResponse : 4D:C1709.Function
property onDataError : 4D:C1709.Function
property onError : 4D:C1709.Function
property onTerminate : 4D:C1709.Function

Class extends _CLI

Class constructor($class : 4D:C1709.Class)
	
	var $controller : 4D:C1709.Class
	var $superclass : 4D:C1709.Class
	$superclass:=$class.superclass
	$controller:=cs:C1710._distill_Controller
	
	While ($superclass#Null:C1517)
		If ($superclass.name=$controller.name)
			$controller:=$class
			break
		End if 
		$superclass:=$superclass.superclass
	End while 
	
	Super:C1705("pdfdistill-pdfcpu"; $controller)
	
Function get worker() : 4D:C1709.SystemWorker
	
	return This:C1470.controller.worker
	
Function terminate()
	
	This:C1470.controller.terminate()
	
Function get controller : cs:C1710._distill_Controller
	
	return This:C1470._controller
	
Function version() : Text
	
	$command:=This:C1470.escape(This:C1470.executablePath)
	$command+=" --version"
	
	This:C1470.onResponse:=Null:C1517
	This:C1470.onData:=Null:C1517
	This:C1470.onDataError:=Null:C1517
	This:C1470.onError:=Null:C1517
	This:C1470.onTerminate:=Null:C1517
	
	This:C1470.controller.clear()
	This:C1470.controller.execute($command)
	This:C1470.worker.wait()
	
	var $stdErr : Text
	$stdErr:=This:C1470.controller.stdErr
	
	var $lines : Collection
	$lines:=Split string:C1554($stdErr; This:C1470.EOL; ck ignore null or empty:K85:5)
	
	ARRAY LONGINT:C221($pos; 0)
	ARRAY LONGINT:C221($len; 0)
	
	var $line : Text
	$line:=$lines.first()
	
	//pdfdistill-pdfcpu cee530f (pdfcpu v0.16.4)\n
	
	If (Match regex:C1019("\\s+([a-f0-9]+)\\s\\("; $line; 1; $pos; $len))
		return Substring:C12($line; $pos{1}; $len{1})
	End if 
	
	return 
	
Function execute($option : Variant; $events : Object) : Collection
	
	var $onResponse; $onData; $onTerminate; $onError : 4D:C1709.Function
	
	var $stdOut; $isStream; $isAsync : Boolean
	var $options : Collection
	var $results : Collection
	$results:=[]
	
	Case of 
		: (Value type:C1509($option)=Is object:K8:27)
			$options:=[$option]
		: (Value type:C1509($option)=Is collection:K8:32)
			If ($option.length#0) && (Value type:C1509($option[0])#Is collection:K8:32)
				$options:=[$option]  //a single argument collection
			Else 
				$options:=$option
			End if 
		Else 
			$options:=[]
	End case 
	
	var $commands : Collection
	$commands:=[]
	
	This:C1470.onResponse:=Null:C1517
	This:C1470.onData:=Null:C1517
	This:C1470.onDataError:=Null:C1517
	This:C1470.onError:=Null:C1517
	This:C1470.onTerminate:=Null:C1517
	
	If ($events#Null:C1517)
		If (OB Instance of:C1731($events.onResponse; 4D:C1709.Function))
			$isAsync:=True:C214
			This:C1470.onResponse:=$events.onResponse
			If ($events.onData#Null:C1517)\
				 && (Value type:C1509($events.onData)=Is object:K8:27)\
				 && (OB Instance of:C1731($events.onData; 4D:C1709.Function))
				This:C1470.onData:=$events.onData
			End if 
			If ($events.onError#Null:C1517)\
				 && (Value type:C1509($events.onError)=Is object:K8:27)\
				 && (OB Instance of:C1731($events.onError; 4D:C1709.Function))
				This:C1470.onError:=$events.onError
			End if 
			If ($events.onDataError#Null:C1517)\
				 && (Value type:C1509($events.onDataError)=Is object:K8:27)\
				 && (OB Instance of:C1731($events.onDataError; 4D:C1709.Function))
				This:C1470.onDataError:=$events.onDataError
			End if 
			If ($events.onTerminate#Null:C1517)\
				 && (Value type:C1509($events.onTerminate)=Is object:K8:27)\
				 && (OB Instance of:C1731($events.onTerminate; 4D:C1709.Function))
				This:C1470.onTerminate:=$events.onTerminate
			End if 
		End if 
	End if 
	
	For each ($option; $options)
		
		If ($option=Null:C1517) || (Value type:C1509($option)#Is collection:K8:32)
			continue
		End if 
		
		$command:=This:C1470.escape(This:C1470.executablePath)
		
		$stdOut:=True:C214
		$isStream:=False:C215
		
		var $data; $file : Variant
		$data:=Null:C1517
		$file:=Null:C1517
		
		var $value : Variant
		For each ($value; $option)
			Case of 
				: (Value type:C1509($value)=Is text:K8:3)
					Case of 
						: ($value="-o") || ($value="--output")
							$stdOut:=False:C215
							$command+=" "+$value
							continue
						: ($value="-")
							$isStream:=True:C214
							$command+=" "+$value
							continue
						Else 
							$command+=" "+This:C1470.escape($value)
							continue
					End case 
					
				: (Value type:C1509($value)=Is real:K8:4)
					$command+=" "+String:C10($value)
					continue
				: (Value type:C1509($value)=Is boolean:K8:9)
					continue
				: (Value type:C1509($value)=Is null:K8:31)
					continue
				: (Value type:C1509($value)=Is object:K8:27)
					If ((OB Instance of:C1731($value; 4D:C1709.File)) || (OB Instance of:C1731($value; 4D:C1709.Folder)))
						$command+=" "+This:C1470.escape(This:C1470.expand($value).path)
					Else 
						If ($value.data#Null:C1517)
							$data:=$value.data
						End if 
						If ($value.file#Null:C1517)\
							 && ((Value type:C1509($value.file)=Is object:K8:27)\
							 && (OB Instance of:C1731($value.file; 4D:C1709.Blob))) || (Value type:C1509($value.file)=Is BLOB:K8:12) || (Value type:C1509($value.file)=Is text:K8:3)
							$file:=$value.file
							
						End if 
					End if 
				Else 
					//
			End case 
		End for each 
		
		If (Not:C34($isAsync))
			This:C1470.controller.clear()
		End if 
		
		var $worker : 4D:C1709.SystemWorker
		$worker:=This:C1470.controller.execute($command; $isStream ? $file : Null:C1517; $data).worker
		
		If (Not:C34($isAsync))
			$worker.wait()
		End if 
		
		If (Not:C34($isAsync))
			var $responseError : Text
			$responseError:=This:C1470.controller.stdErr
			var $response : 4D:C1709.Blob
			$response:=This:C1470.controller.stdOut
			$results.push({response: $response; responseError: $responseError; exitCode: $worker.exitCode})
		End if 
		
	End for each 
	
	If (Not:C34($isAsync))
		return $results
	End if 