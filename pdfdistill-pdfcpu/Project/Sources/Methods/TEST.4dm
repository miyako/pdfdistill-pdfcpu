//%attributes = {}
#DECLARE($params : Object)

If ($params=Null:C1517)
	
	CALL WORKER:C1389(1; Current method name:C684; {})
	
Else 
	
	var $distill : cs:C1710.distill
	$distill:=cs:C1710.distill.new(cs:C1710._distill_Controller)
	
	$pdf:=File:C1566(System folder:C487(Desktop:K41:16)+"sample.pdf"; fk platform path:K87:2)
	$out:=File:C1566(System folder:C487(Desktop:K41:16)+"sample.distilled.pdf"; fk platform path:K87:2)
	
	$events:={}
	$events.onResponse:=Formula:C1597(ALERT:C41($1.responseError))
	$events.onResponse:=Formula:C1597(onResponse)
	$tasks:=[]
/*
any element that is an object not a file or folder is considered a context object
context object can have 2 properties: .data, .file
.data is a variant that is passed to the callback 
.file is used as the stdin (assuming you pass -)
it can be 4D.File, 4D.Blob, Blob, or Text
*/
	
	//file in, file out
	$tasks.push([$pdf; "-o"; $out; \
		"--quality"; 50; \
		"--max-dpi"; 150; \
		"--lossless"; {data: $out; file: Null:C1517}])
	
	//blob in, blob out
	$tasks.push(["-"; \
		"--quality"; 50; \
		"--max-dpi"; 150; \
		"--lossless"; {data: $out; file: $pdf.getContent()}])
	
	If (True:C214)  //async
		$distill.execute($tasks; $events)
	Else   //sync
		$results:=$distill.execute($tasks)
	End if 
	
End if 