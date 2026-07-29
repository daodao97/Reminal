class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "1.9.4"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.4/reminal_1.9.4_darwin_arm64.tar.gz"
      sha256 "6a761bf3939e43bb345a1befcac404c1492ad66cb62fe5adc78caa38b332beb6"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.4/reminal_1.9.4_darwin_amd64.tar.gz"
      sha256 "7728e64ada6c49180ec12abc0c1e232afc59903554c33d3a3fa880546e29d796"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.4/reminal_1.9.4_linux_arm64.tar.gz"
      sha256 "9e93e0c51cc5d07d221e405a91476c8d98d0939238d67c6ba33654afc2626f61"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.4/reminal_1.9.4_linux_amd64.tar.gz"
      sha256 "18e63b94962d6cc91c0003e782b96abff793b811f3519a69f9e68b3708ec2cdd"
    end
  end

  depends_on "go" => :build if build.head?

  def install
    if build.head?
      system "go", "build", "-ldflags=#{ldflags}", "-o", bin/"reminal", "./cmd/reminal"
    else
      bin.install "reminal"
    end
  end

  def ldflags
    "-s -w " \
      "-X main.version=#{version} " \
      "-X github.com/reminal/reminal/internal/config.DefaultCloudRelay=wss://reminal-relay.futuristic.workers.dev/ws " \
      "-X github.com/reminal/reminal/internal/config.DefaultCloudWeb=https://reminal-relay.futuristic.workers.dev"
  end

  def caveats
    <<~EOS
      reminal connects to the hosted relay automatically — no setup needed.

        reminal              # share your terminal
        reminal --connect ID --pin PIN
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/reminal version")
  end
end
