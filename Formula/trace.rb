class Trace < Formula
  desc "Local coding-agent history and rewind"
  homepage "https://usetrace.si"
  version "1.1.0-solo.4"
  license "Apache-2.0"

  on_macos do
    on_arm do
      url "https://usetrace.si/downloads/v1.1.0-solo.4/trace_1.1.0-solo.4_darwin_arm64.tar.gz"
      sha256 "9c2c17c46774b9db6b4ab9cb40ca209ca41b3a32d8e3326e02df61fb138e1659"
    end
    on_intel do
      url "https://usetrace.si/downloads/v1.1.0-solo.4/trace_1.1.0-solo.4_darwin_amd64.tar.gz"
      sha256 "c0868f04fdd487623780ff97674597a6e9ff9adbc5d0949adf9f60b5328ee9c8"
    end
  end

  on_linux do
    on_arm do
      url "https://usetrace.si/downloads/v1.1.0-solo.4/trace_1.1.0-solo.4_linux_arm64.tar.gz"
      sha256 "548e29e213a307c6ebf57c4089505cdbed2954d2487730264ebc446227194ddc"
    end
    on_intel do
      url "https://usetrace.si/downloads/v1.1.0-solo.4/trace_1.1.0-solo.4_linux_amd64.tar.gz"
      sha256 "23ec1527f1d7e68654a4e3a9ed8231bead1696eefdd70b510048a1ecddaa5ccb"
    end
  end

  def install
    bin.install "trace", "trace-brain", "trace-graph"
    bin.install_symlink bin/"trace" => "trc"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/trace version")
    assert_match version.to_s, shell_output("#{bin}/trace-brain version")
    assert_match version.to_s, shell_output("#{bin}/trace-graph version")
  end
end
